/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

#include <config.h>
#include <xpl.h>
#include <connio.h>
#include <bongoutil.h>
#include <memmgr.h>
#include <mdb.h>
#include <msgapi.h>
#include <management.h>

#define MAX_CONNECT_ATTEMPTS 120

static int ManagementRegister(void);
static void ManagementRegisterThread(void *param);
static void ManagementUnRegister(void);
static void ManagementDMCRegister(void);
static void ManagementDMCUnRegister(void);
static int ManagementDMCHeartBeat(void);

enum REGISTER_STATES {
    REGISTER_READY = 0,                     /* exclusive state */
    REGISTER_REGISTERED = (1 << 0),
    REGISTER_STOPPING = (1 << 1),
    REGISTER_STOPPED = (1 << 2)             /* exclusive state */
};

struct {
    enum ManagementStates state;

    struct {
        XplThreadID id;
        enum REGISTER_STATES state;
        unsigned short port;
        Connection *pConn;
        TraceDestination *traceDestination;

        unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char credential[NMAP_HASH_SIZE + 1];
    } dmc;

    struct {
        Connection *conn;

        XplAtomic active;

        unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char signature[36];

        struct {
            unsigned long count;

            ManagementVariables *data;
        } variables;

        struct {
            unsigned long count;

            ManagementCommands *data;
        } commands;
    } server;

    BongoKeywordIndex *traceFlagIndex;

    MDBHandle directoryHandle;
} Management;

void 
MsgSendTrap(int TrapType, unsigned char *TrapMessage)
{
    int ccode;
    unsigned long length;
    unsigned char buffer[CONN_BUFSIZE + 1];
    Connection *conn = NULL;

    if (TrapMessage) {
        length = strlen(TrapMessage);

        conn = ConnAlloc(TRUE);
        if (conn) {
            ccode = ConnSocket(conn);

            if (ccode != -1) {
                conn->socketAddress.sin_family = AF_INET;
                conn->socketAddress.sin_addr.s_addr = MsgGetHostIPAddress();
                conn->socketAddress.sin_port = Management.dmc.port;

                ccode = ConnConnect(conn, NULL, 0, NULL);
            }

            if (ccode != -1) {
                ccode = ConnRead(conn, buffer, sizeof(buffer));
            }

            if (ccode != -1) {
                ccode = ConnWriteF(conn, "TRAP %s %lu\r\n", Management.server.signature, length);
            }

            if (ccode != -1) {
                ccode = ConnFlush(conn);
            }

            if (ccode != -1) {
                ccode = ConnRead(conn, buffer, sizeof(buffer));
            }

            if (ccode != -1) {
                if (XplStrNCaseCmp(buffer, DMCMSG1003SEND_BYTES, 10) == 0) {
                    ccode = ConnWrite(conn, TrapMessage, length);
                } else {
                    ccode = -1;
                }
            }

            ConnWrite(conn, "QUIT\r\n", 6);
            ConnFlush(conn);
            ConnRead(conn, buffer, sizeof(buffer));
            ConnClose(conn, 0);
            ConnFree(conn);
            conn = NULL;
        }
    }

    return;
}

typedef struct {
    unsigned char *name;
    unsigned long len;
    unsigned long flag;
    BOOL add;
} TraceOption;


static TraceOption TraceOptions[] = {
    {"+ALL", sizeof("+ALL") - 1, CONN_TRACE_ALL, TRUE},
    {"-ALL", sizeof("-ALL") - 1, CONN_TRACE_ALL, FALSE},
    {"+INBOUND", sizeof("+INBOUND") - 1, CONN_TRACE_SESSION_INBOUND, TRUE},
    {"-INBOUND", sizeof("-INBOUND") - 1, CONN_TRACE_SESSION_INBOUND, FALSE},
    {"+OUTBOUND", sizeof("+OUTBOUND") - 1, CONN_TRACE_SESSION_OUTBOUND, TRUE},
    {"-OUTBOUND", sizeof("-OUTBOUND") - 1, CONN_TRACE_SESSION_OUTBOUND, FALSE},
    {"+NMAP", sizeof("+NMAP") - 1, CONN_TRACE_SESSION_NMAP, TRUE},
    {"-NMAP", sizeof("-NMAP") - 1, CONN_TRACE_SESSION_NMAP, FALSE},
    {"+CONNECT", sizeof("+CONNECT") - 1, CONN_TRACE_EVENT_CONNECT, TRUE},
    {"-CONNECT", sizeof("-CONNECT") - 1, CONN_TRACE_EVENT_CONNECT, FALSE},
    {"+CLOSE", sizeof("+CLOSE") - 1, CONN_TRACE_EVENT_CLOSE, TRUE},
    {"-CLOSE", sizeof("-CLOSE") - 1, CONN_TRACE_EVENT_CLOSE, FALSE},
    {"+SSLCONNECT", sizeof("+SSLCONNECT") - 1, CONN_TRACE_EVENT_SSL_CONNECT, TRUE},
    {"-SSLCONNECT", sizeof("-SSLCONNECT") - 1, CONN_TRACE_EVENT_SSL_CONNECT, FALSE},
    {"+SSLSHUTDOWN", sizeof("+SSLSHUTDOWN") - 1, CONN_TRACE_EVENT_SSL_SHUTDOWN, TRUE},
    {"-SSLSHUTDOWN", sizeof("-SSLSHUTDOWN") - 1, CONN_TRACE_EVENT_SSL_SHUTDOWN, FALSE},
    {"+READ", sizeof("+READ") - 1, CONN_TRACE_EVENT_READ, TRUE},
    {"-READ", sizeof("-READ") - 1, CONN_TRACE_EVENT_READ, FALSE},
    {"+WRITE", sizeof("+WRITE") - 1, CONN_TRACE_EVENT_WRITE, TRUE},
    {"-WRITE", sizeof("-WRITE") - 1, CONN_TRACE_EVENT_WRITE, FALSE},
    {"+ERROR", sizeof("+ERROR") - 1, CONN_TRACE_EVENT_ERROR, TRUE},
    {"-ERROR", sizeof("-ERROR") - 1, CONN_TRACE_EVENT_ERROR, FALSE},
    {NULL, 0, 0, 0}
};


BOOL 
ManagementConnTrace(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection)
{
    if (CloseConnection) {
        *CloseConnection = FALSE;
    }

    if (Response) {
        if (ConnTraceAvailable()) {
            if (Arguments) {
                char *ptr;

                if (XplStrNCaseCmp(Arguments, "FLAGS ", strlen("FLAGS ")) == 0) {
                    unsigned long flags = CONN_TRACE_GET_FLAGS();
                    long flagId;

                    ptr = Arguments + strlen("FLAGS ");
                    
                    do {
                        flagId = BongoKeywordBegins(Management.traceFlagIndex, ptr);
                        if (flagId != -1) {
                            if (TraceOptions[flagId].add) {
                                flags |= TraceOptions[flagId].flag;
                            } else {
                                flags &= ~(TraceOptions[flagId].flag);
                            }
                            ptr += TraceOptions[flagId].len;

                            while(isspace(*ptr)) {
                                ptr++;
                            }

                            continue;
                        }

                        *Response = strdup(DMCMC_CONN_TRACE_USAGE_HELP);
                        return(TRUE);
                    } while (*ptr);

                    CONN_TRACE_SET_FLAGS(flags);
                    *Response = strdup("ConnTrace flags updated\r\n");
                    return(TRUE);
                }
            
                if (XplStrNCaseCmp(Arguments, "ADDR ", sizeof("ADDR ") - 1) == 0) {
                    *Response = strdup("ConnTrace addr not implemented\r\n");
                   return(TRUE);
                } 
            }
        
            *Response = strdup(DMCMC_CONN_TRACE_USAGE_HELP);
            return(TRUE);
        }

        *Response = strdup("ConnTrace not supported!\r\n");
        return(TRUE);
    }
    return(FALSE);
}

BOOL 
ManagementMemoryStats(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection)
{
    int length;
    unsigned long x;
    unsigned long y;
    unsigned long total = 0;
    unsigned char *base;
    unsigned char *cur;
    BOOL result = TRUE;
    MemStatistics *stats = NULL;
    MemStatistics *next;

    if (CloseConnection) {
        *CloseConnection = FALSE;
    }

    if (Response) {
        if (!Arguments) {
            stats = MemAllocStatistics();
            if (stats) {
                base = cur = NULL;
                length = 0;
                next = stats;

                do {
                    cur = MemRealloc(base, length + 192);
                    if (cur) {
                        base = cur;
                        cur = base + length;

                        total += next->totalAlloc.size;

                        if (next->pitches) {
                            x = (next->hits * 100) / next->pitches;
                            y = (next->hits * 100) % next->pitches;
                        } else {
                            x = y = 0;
                        }

                        length += sprintf(cur, "%s: %lu [%lu@%lu] %lum %luM %lu.%02lu%%\r\n", next->name, next->totalAlloc.size, next->entry.allocated, next->entry.size, next->entry.minimum, next->entry.maximum, x, y);

                        next = next->next;

                        continue;
                    }

                    MemFree(base);

                    base = MemStrdup("Out of memory.\r\n");

                    result = FALSE;

                    break;
                } while (next);

                if (result) {
                    cur = MemRealloc(base, length + 32);
                    if (cur) {
                        base = cur;
                        cur = base + length;

                        length += sprintf(cur, "Total allocated = %lu\r\n", total);
                    }
                }

                *Response = base;

                MemFreeStatistics(stats);
            } else {
                *Response = MemStrdup("Out of memory.\r\n");
            }
        } else {
            *Response = MemStrdup("Arguments not allowed.\r\n");
        }

        if (*Response) {
            return(TRUE);
        }
    }

    return(FALSE);
}

static void 
HandleManagementClient(void *param)
{
    int ccode;
    size_t count;
    unsigned long index = 0;
    size_t bufferSize = 0;
    size_t readSize;
    unsigned char *response = NULL;
    unsigned char *ptr;
    unsigned char *ptr2;
    unsigned char buffer[CONN_BUFSIZE + 1];
    BOOL close = FALSE;
    Connection *conn = param;

    if (conn) {
        /* Set TCP non blocking io */
        ccode = 1;
        setsockopt(conn->socket, IPPROTO_TCP, 1, (unsigned char *)&ccode, sizeof(ccode));
    } else {
        XplSafeDecrement(Management.server.active);

        XplExitThread(TSR_THREAD, 0);

        return;
    }

    ConnWrite(conn, DMCMSG1000OK, DMCMSG1000OK_LEN);
    ConnFlush(conn);
    

    do {
        if (ConnReadAnswer(conn, buffer, sizeof(buffer)) < 0) {
            break;
        }
        /*    Process Client request    */
        switch(toupper(buffer[0])) {
            case 'C': {
                /* CMD <CommandIndex>[ Arguments] */
                if (buffer[3] == ' ') {
                    ptr = strchr(buffer + 4, ' ');
                    if (ptr) {
                        *ptr++ = '\0';
                    }

                    index = atol(buffer + 4);
                    if (index < Management.server.commands.count) {
                        ptr2 = NULL;
                        if (Management.server.commands.data[index].Execute(ptr, &ptr2, &close)) {
                            if (ptr2) {
                                count = strlen(ptr2);
                            } else {
                                count = 0;
                            }

                            ConnWriteF(conn, DMCMSG1001BYTES_COMING, count);
                            ConnWrite(conn, ptr2, count);
                            ConnWrite(conn, DMCMSG1000OK, DMCMSG1000OK_LEN);

                            if (ptr2) {
                                MemFree(ptr2);
                                ptr2 = NULL;
                            }

                            break;
                        }
                    }

                    ConnWrite(conn, DMCMSG2101BADPARAMS, DMCMSG2101BADPARAMS_LEN);
                    break;
                }

                ConnWrite(conn, DMCMSG2100BADCOMMAND, DMCMSG2100BADCOMMAND_LEN);
                break;
            }

            case 'E': {
                /* CORE */
                if (strcmp(buffer, "EROC") == 0) {
                    /*    Induce a segmentation fault.    */
                    ptr = NULL;
                    *ptr = 'C';
                }

                ConnWrite(conn, DMCMSG2100BADCOMMAND, DMCMSG2100BADCOMMAND_LEN);
                break;
            }

            case 'H': {
                /* HELP <VariableIndex> */
                if (buffer[4] == ' ') {
                    index = atol(buffer + 5);
                    if (index < Management.server.variables.count) {
                        count = strlen(Management.server.variables.data[index].Help);

                        ConnWriteF(conn, DMCMSG1001BYTES_COMING, count);
                        ConnWrite(conn, Management.server.variables.data[index].Help, count);
                        ConnWrite(conn, DMCMSG1000OK, DMCMSG1000OK_LEN);

                        break;
                    }

                    ConnWrite(conn, DMCMSG2101BADPARAMS, DMCMSG2101BADPARAMS_LEN);
                    break;
                }

                ConnWrite(conn, DMCMSG2100BADCOMMAND, DMCMSG2100BADCOMMAND_LEN);
                break;
            }

            case 'Q': {
                /* QUIT */
                close = TRUE;
                ConnWrite(conn, DMCMSG1000OK, DMCMSG1000OK_LEN);
                continue;
            }

            case 'R': {
                /* READ <VariableIndex> */
                if (buffer[4] == ' ') {
                    index = atol(buffer + 5);
                    if (index < Management.server.variables.count) {
                        do {
                            readSize = bufferSize;
                            if (Management.server.variables.data[index].Read(index, response, &readSize)) {
                                if (readSize < bufferSize) {
                                    ConnWriteF(conn, DMCMSG1001BYTES_COMING, readSize);
                                    ConnWrite(conn, response, readSize);
                                    ConnWrite(conn, DMCMSG1000OK, DMCMSG1000OK_LEN);
                                    break;
                                }

                                ptr = (unsigned char *)MemRealloc(response, readSize + 32);
                                if (ptr) {
                                    response = ptr;
                                    bufferSize = readSize + 32;
                                    continue;
                                }

                                ConnWrite(conn, DMCMSG2000NOMEMORY, DMCMSG2000NOMEMORY_LEN);
                                break;
                            }

                            ConnWrite(conn, DMCMSG2002READ_ERROR, DMCMSG2002READ_ERROR_LEN);
                            break;
                        } while (TRUE);

                        break;
                    }

                    ConnWrite(conn, DMCMSG2101BADPARAMS, DMCMSG2101BADPARAMS_LEN);
                    break;
                }

                ConnWrite(conn, DMCMSG2100BADCOMMAND, DMCMSG2100BADCOMMAND_LEN);
                break;
            }
            case 'S': {
                XplConsolePrintf("%s: Shutdown\n", Management.server.dn);
            }

            case 'W': {
                /* WRITE <VariableIndex> <Length> */
                if ((buffer[5] == ' ') && (isdigit(buffer[6]))) {
                    ptr = strchr(buffer + 6, ' ');
                    if (ptr && (isdigit(ptr[1]))) {
                        *ptr++ = '\0';
                        index = atol(buffer + 6);
                        readSize = atol(ptr);

                        if (index < Management.server.variables.count) {
                            if (Management.server.variables.data[index].Write) {
                                do {
                                    if (readSize < bufferSize) {
                                        ConnWrite(conn, DMCMSG1003SEND_VALUE, DMCMSG1003SEND_VALUE_LEN);
                                        ConnFlush(conn);

                                        ConnReadCount(conn, response, readSize);

                                        if (Management.server.variables.data[index].Write(index, response, readSize)) {
                                            ConnWrite(conn, DMCMSG1000OK, DMCMSG1000OK_LEN);
                                            break;
                                        }

                                        ConnWrite(conn, DMCMSG2002WRITE_ERROR, DMCMSG2002WRITE_ERROR_LEN);
                                        break;
                                    }

                                    ptr = (unsigned char *)MemRealloc(response, readSize + 32);
                                    if (ptr) {
                                        response = ptr;
                                        bufferSize = readSize + 32;
                                        continue;
                                    }

                                    ConnWrite(conn, DMCMSG2000NOMEMORY, DMCMSG2000NOMEMORY_LEN);
                                    break;
                                } while (TRUE);

                                break;
                            }

                            ConnWrite(conn, DMCMSG2104NOT_WRITEABLE, DMCMSG2104NOT_WRITEABLE_LEN);
                            break;
                        }
                    }

                    ConnWrite(conn, DMCMSG2101BADPARAMS, DMCMSG2101BADPARAMS_LEN);
                    break;
                }

                ConnWrite(conn, DMCMSG2100BADCOMMAND, DMCMSG2100BADCOMMAND_LEN);
                break;
            }

            default: {
                ConnWrite(conn, DMCMSG2100BADCOMMAND, DMCMSG2100BADCOMMAND_LEN);
                break;
            }
        }

        ConnFlush(conn);
    } while ((Management.state == MANAGEMENT_RUNNING) && !close);

    ConnClose(conn, 0);

    if (response != NULL) {
        MemFree(response);
        response = NULL;
    }

    XplSafeDecrement(Management.server.active);

    ConnFree(conn);
    conn = NULL;

    XplExitThread(TSR_THREAD, 0);
    return;
}

BOOL 
ManagementInit(const unsigned char *Identity, MDBHandle DirectoryHandle)
{
    unsigned long used;
    MDBValueStruct *config = NULL;
    BOOL result = FALSE;

    Management.state = MANAGEMENT_LOADING;

    XplSafeWrite(Management.server.active, 0);

    Management.dmc.dn[0] = '\0';
    Management.server.dn[0] = '\0';

    memset(Management.dmc.credential, 0, sizeof(Management.dmc.credential));
    memset(Management.server.signature, 0, sizeof(Management.server.signature));

    Management.directoryHandle = NULL;

    if (Identity && Identity[0] && DirectoryHandle) {
        Management.state = MANAGEMENT_INITIALIZING;
        Management.directoryHandle = DirectoryHandle;

        result = TRUE;
    }

    if (result) {
        result = FALSE;

        config = MDBCreateValueStruct(Management.directoryHandle, NULL);

        if (config) {
            result = TRUE;
        }
    }

    if (result) {
        result = FALSE;

	result = MsgGetConfigProperty(Management.dmc.dn, 
				      MSGSRV_CONFIG_PROP_MESSAGING_SERVER);
    }

    if (result) {
        MDBSetValueStructContext(Management.dmc.dn, config);

        result = MDBIsObject(Identity, config);
        if (result) {
            sprintf(Management.server.dn, "%s\\%s", Management.dmc.dn, Identity);
        }

        MDBSetValueStructContext(NULL, config);
    }

    if (result) {
        result = FALSE;

        Management.dmc.port = htons(DMC_MANAGER_PORT);

        if (MDBRead(Management.dmc.dn, MSGSRV_A_CONFIGURATION, config)) {
            for (used = 0; used < config->Used; used++) {
                if ((XplStrNCaseCmp(config->Value[used], "DMC: ManagerPort=", 20) == 0) 
                        && (isdigit(config->Value[used][20]))) {
                    Management.dmc.port = htons((unsigned short)(atol(config->Value[used] + 20)));
                }
            }

            MDBFreeValues(config);
        }

        if (MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, config)) {
            result = HashCredential(Management.dmc.dn, config->Value[0], Management.dmc.credential);

            MDBFreeValues(config);
        }
    }

    if (config) {
        MDBDestroyValueStruct(config);
        config = NULL;
    }

    BongoKeywordIndexCreateFromTable(Management.traceFlagIndex, TraceOptions, .name, TRUE); 
    if (result) {
        Management.state = MANAGEMENT_READY;
    } else {
        Management.state = MANAGEMENT_STOPPED;
    }

    return(result);
}

void 
ManagementShutdown(void)
{
    if (Management.state < MANAGEMENT_STOPPING) {

        if (Management.state == MANAGEMENT_RUNNING) {
            Management.state = MANAGEMENT_STOPPING;
        } else {
            /* If the state was never set to "RUNNING" there's nothing to stop */
            Management.state = MANAGEMENT_STOPPED;
        }

        if (Management.traceFlagIndex) {
            BongoKeywordIndexFree(Management.traceFlagIndex);
        }
        
        if ((Management.server.conn) && (Management.server.conn->socket != -1)) {
            IPshutdown(Management.server.conn->socket, 2);
            IPclose(Management.server.conn->socket);
            Management.server.conn->socket = -1;
        }

    }

    return;
}

enum ManagementStates ManagementState(void)
{
    return(Management.state);
}

BOOL 
ManagementSetVariables(ManagementVariables *Variables, unsigned long Count)
{
    if (Management.state == MANAGEMENT_READY) {
        Management.server.variables.count = Count;
        Management.server.variables.data = Variables;
        return(TRUE);
    }

    return(FALSE);
}

BOOL 
ManagementSetCommands(ManagementCommands *Commands, unsigned long Count)
{
    if (Management.state == MANAGEMENT_READY) {
        Management.server.commands.count = Count;
        Management.server.commands.data = Commands;
        return(TRUE);
    }

    return(FALSE);
}

void 
ManagementServer(void *param)
{
    int i;
    int ccode;
    unsigned long used;
    unsigned char buffer[CONN_BUFSIZE + 1];
    unsigned char *name = NULL;
    XplThreadID id = 0;
    Connection *conn = NULL;

    XplSignalBlock();

    if (Management.state == MANAGEMENT_READY) {
        name = strrchr(Management.server.dn, '\\');

        if (name) {
            name++;
        } else {
            name = Management.server.dn;
        }

        sprintf(buffer, "%s Management Server", name);
        XplRenameThread(XplGetThreadID(), buffer);

        Management.server.conn = ConnAlloc(FALSE);
        if (Management.server.conn) {
            Management.server.conn->socketAddress.sin_family = AF_INET;
            /* Listen on the loopback device */
            Management.server.conn->socketAddress.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
        } else {
            Management.state = MANAGEMENT_STOPPING;
        }
    }

    if (Management.state == MANAGEMENT_READY) {
        if (ConnServerSocket(Management.server.conn, 2048) == -1) {
            Management.state = MANAGEMENT_STOPPING;
        }
    }

    ManagementRegister();

    /*    Listen for management requests    */
    while (Management.state == MANAGEMENT_RUNNING) {
       conn = ConnAlloc(TRUE);
        if (conn) {
            ccode = sizeof(conn->socketAddress);
            conn->socket = IPaccept(Management.server.conn->socket, (struct sockaddr *)&(conn->socketAddress), &ccode);

            if (conn->socket != -1) {
                if (Management.state == MANAGEMENT_RUNNING) {
                    XplBeginCountedThread(&id, HandleManagementClient, DMC_MANAGEMENT_STACKSIZE, conn, ccode, Management.server.active);

                    if (!ccode) {
                        continue;
                    }
                }

                ConnSend(conn, "QUIT\r\n", 6);

                ConnClose(conn, 0);

                ConnFree(conn);
                conn = NULL;

                continue;
            }

            ConnFree(conn);
            conn = NULL;

            switch (errno) {
                case ECONNABORTED:
#ifdef EPROTO
                case EPROTO:
#endif
                case EINTR: {
                    continue;
                }

                default: {
                    Management.state = MANAGEMENT_STOPPING;

                    break;
                }
            }
        }

        /* Shutdown! */
        break;
    }

    /* Shutting down */
    Management.state = MANAGEMENT_STOPPING;

    for (used = 0; XplSafeRead(Management.server.active) && (used < 60); i++) {
        XplDelay(1000);
    }
    
    ManagementUnRegister();
    Management.state = MANAGEMENT_STOPPED;

    XplExitThread(TSR_THREAD, 0);
}

static int
ManagementRegister()
{
    int err;

    if (getenv("BONGO_NO_DMC")) {
        return 0;
    }

    Management.dmc.state = REGISTER_READY;
    XplBeginThread(
            &Management.dmc.id,
            ManagementRegisterThread,
            DMC_MANAGEMENT_STACKSIZE,
            0,
            err);
    Management.state = MANAGEMENT_RUNNING;
    return(err);
}

static void
ManagementRegisterThread(void *param)
{
    while (Management.dmc.state < REGISTER_STOPPING) {
        ManagementDMCRegister();               /* blocking until registered */

        while (Management.dmc.state == REGISTER_REGISTERED) {
            if (ManagementDMCHeartBeat()) {
                int i;
                for (i = 0; i < 5 && Management.dmc.state < REGISTER_STOPPING; i++) {
                    XplDelay(1000);         /* every five seconds */
                }
            } else {
                break;
            }
        }
    }
    if (Management.dmc.state & REGISTER_REGISTERED) {
        ManagementDMCUnRegister();
    }
    Management.dmc.state = REGISTER_STOPPED;
    XplExitThread(TSR_THREAD, 0);
}

static void
ManagementUnRegister(void)
{
    if (getenv("BONGO_NO_DMC")) {
        return;
    }

    Management.dmc.state |= REGISTER_STOPPING;
    while (Management.dmc.state != REGISTER_STOPPED) {
        XplDelay(300);
    }
}

static void
ManagementDMCRegister(void)
{
    int err;
    unsigned long used;
    xpl_hash_context ctx;
    unsigned char buffer[CONN_BUFSIZE + 1];

    if (Management.dmc.pConn == NULL) {
        Management.dmc.pConn = ConnAlloc(TRUE);
    } else {
        if (Management.dmc.pConn->socket != -1) {
            IPshutdown(Management.dmc.pConn->socket, 2);
            IPclose(Management.dmc.pConn->socket);
            Management.dmc.pConn->socket = -1;
        }
    }
    if (Management.dmc.pConn) {
        Management.dmc.pConn->bSelfManage = TRUE;
        Management.dmc.pConn->socketAddress.sin_family = AF_INET;
        Management.dmc.pConn->socketAddress.sin_addr.s_addr =
            htonl(INADDR_LOOPBACK);
        Management.dmc.pConn->socketAddress.sin_port = Management.dmc.port;
        err = ConnSocket(Management.dmc.pConn);
        if (err != -1) {
            Management.dmc.traceDestination = CONN_TRACE_CREATE_DESTINATION(CONN_TYPE_OUTBOUND);
            /* Register with the Bongo Distributed Management Console */
            while (Management.dmc.state < REGISTER_STOPPING) {
                if (ConnConnectEx(
                            Management.dmc.pConn,
                            NULL,
                            0,
                            NULL,
                            Management.dmc.traceDestination) != -1) {
                    break;
                } else {
                    int i;
                    for (i = 0; i < 5 && Management.dmc.state < REGISTER_STOPPING; i++) {
                        XplDelay(1000);         /* every five seconds */    
                    }
                    continue;
                }
            }
            if (Management.dmc.state < REGISTER_STOPPING) {
                err = ConnRead(Management.dmc.pConn, buffer, sizeof(buffer));
                if (err != -1) {
                    err = ConnWriteF(
                            Management.dmc.pConn,
                            "REGISTER \"%s\" %d -1\r\n",
                            Management.server.dn,
                            ntohs(Management.server.conn->socketAddress.sin_port));
                }
                if (err != -1) {
                    err = ConnFlush(Management.dmc.pConn);
                }
                if (err != -1) {
                    err = ConnReadAnswer(
                            Management.dmc.pConn,
                            buffer,
                            sizeof(buffer));
                }
                if (err != -1) {
                    if (strncmp(
                                buffer,
                                DMCMSG1002SALT_COMING,
                                DMCMSG1002SALT_COMING_LEN - 2) == 0) {
                        err = ConnReadAnswer(
                                Management.dmc.pConn,
                                buffer,
                                sizeof(buffer));
                    } else {
                        err = -1;
                    }
                }
                if (err != -1) {
                    /* Generate and send our signature */
                    XplHashNew(&ctx, XPLHASH_MD5);
                    XplHashWrite(&ctx, buffer, strlen(buffer));
                    XplHashWrite(&ctx, Management.dmc.credential, NMAP_HASH_SIZE);
                    XplHashWrite(&ctx, Management.server.dn, strlen(Management.server.dn));
                    XplHashFinal(&ctx, XPLHASH_LOWERCASE, 
                          Management.server.signature, XPLHASH_MD5_LENGTH);

                    err = ConnWriteF(
                            Management.dmc.pConn,
                            "%s\r\n",
                            Management.server.signature);
                }
                if (err != -1) {
                    err = ConnFlush(Management.dmc.pConn);
                }
                if (err != -1) {
                    err = ConnReadAnswer(
                            Management.dmc.pConn,
                            buffer,
                            sizeof(buffer));
                }
                if (err != -1) {
                    if (strncmp(
                                buffer,
                                DMCMSG1003SEND_VARIABLES,
                                DMCMSG1003SEND_VARIABLES_LEN - 2) == 0) {
                        for (
                                used = 0;
                                (used < Management.server.variables.count) &&
                                (err != -1);
                                used++) {
                            err = ConnWriteF(
                                    Management.dmc.pConn,
                                    "%c \"%s\"\r\n",
                                    (Management.server.variables.data[used].Write == NULL) ? 'R': 'W',
                                    Management.server.variables.data[used].Name);
                        }
                        if (err != -1) {
                            err = ConnWrite(Management.dmc.pConn, "END\r\n", 5);
                        }
                    } else {
                        err = -1;
                    }
                }
                if (err != -1) {
                    err = ConnFlush(Management.dmc.pConn);
                }
                if (err != -1) {
                    err = ConnReadAnswer(
                            Management.dmc.pConn,
                            buffer,
                            sizeof(buffer));
                }
                if (err != -1) {
                    if (strncmp(
                                buffer,
                                DMCMSG1003SEND_COMMANDS,
                                DMCMSG1003SEND_COMMANDS_LEN - 2) == 0) {
                        for (
                                used = 0;
                                (used < Management.server.commands.count) &&
                                (err != -1);
                                used++) {
                            err = ConnWriteF(
                                    Management.dmc.pConn,
                                    "%s\r\n",
                                    Management.server.commands.data[used].Name);
                        }
                        if (err != -1) {
                            err = ConnWrite(Management.dmc.pConn, "END\r\n", 5);
                        }
                    } else {
                        err = -1;
                    }
                }
                if (err != -1) {
                    err = ConnFlush(Management.dmc.pConn);
                }
                if (err != -1) {
                    err = ConnReadAnswer(
                            Management.dmc.pConn,
                            buffer,
                            sizeof(buffer));
                }
                if (err != -1) {
                    if (strncmp(buffer, DMCMSG1000OK, DMCMSG1000OK_LEN - 2) != 0) {
                        err = -1;
                    }
                }
                if (err != -1) {
                    Management.dmc.state = REGISTER_REGISTERED;
                } else {
                    ConnWriteF(
                            Management.dmc.pConn,
                            "DEREGISTER %s\r\n",
                            Management.server.signature);
                    ConnFlush(Management.dmc.pConn);
                    ConnRead(Management.dmc.pConn, buffer, sizeof(buffer));
                }
            }
        }
    }
}

static void
ManagementDMCUnRegister()
{
    unsigned char buffer[CONN_BUFSIZE + 1];

    if ((Management.dmc.pConn) && (Management.dmc.pConn->socket != -1)) {
//        ConnRead(Management.dmc.pConn, buffer, sizeof(buffer));
        ConnWriteF(
                Management.dmc.pConn,
                "DEREGISTER %s\r\n",
                Management.server.signature);
        ConnFlush(Management.dmc.pConn);
        ConnRead(Management.dmc.pConn, buffer, sizeof(buffer));

        ConnWriteF(Management.dmc.pConn, "QUIT\r\n");
        ConnFlush(Management.dmc.pConn);

        ConnClose(Management.dmc.pConn, 0);
        CONN_TRACE_FREE_DESTINATION(Management.dmc.traceDestination);
        ConnFree(Management.dmc.pConn);
        Management.dmc.pConn = NULL;
    }
    Management.dmc.state &= ~REGISTER_REGISTERED;
}

static int
ManagementDMCHeartBeat(void)
{
    int err;
    unsigned char buffer[CONN_BUFSIZE + 1];

    err = ConnWriteF(Management.dmc.pConn, "HEARTBEAT\r\n");
    ConnFlush(Management.dmc.pConn);

    buffer[0] = '\0';
    err = ConnReadAnswer(Management.dmc.pConn, buffer, sizeof(buffer));
    if (err != -1) {
        if (strncmp(buffer, DMCMSG1000OK, DMCMSG1000OK_LEN - 2) != 0) {
            err = 0;
        }
    } else {
        err = 0;
    }

    return(err);
}

