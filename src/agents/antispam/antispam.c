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
 * (C) 2007 Patrick Felt
 ****************************************************************************/

/** \file antispam.c Code for the anti-spam agent
 */

#include <config.h>

#include <xpl.h>
#include <memmgr.h>
#include <logger.h>
#include <bongoagent.h>
#include <bongoutil.h>
#include <mdb.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <streamio.h>
#include <connio.h>

#include "antispam.h"

#if defined(RELEASE_BUILD)
#define ASpamClientAlloc() MemPrivatePoolGetEntry(ASpam.nmap.pool)
#else
#define ASpamClientAlloc() MemPrivatePoolGetEntryDebug(ASpam.nmap.pool, __FILE__, __LINE__)
#endif

static void SignalHandler(int sigtype);

#define QUEUE_WORK_TO_DO(c, id, r) \
        { \
            XplWaitOnLocalSemaphore(ASpam.nmap.semaphore); \
            if (XplSafeRead(ASpam.nmap.worker.idle)) { \
                (c)->queue.previous = NULL; \
                if (((c)->queue.next = ASpam.nmap.worker.head) != NULL) { \
                    (c)->queue.next->queue.previous = (c); \
                } else { \
                    ASpam.nmap.worker.tail = (c); \
                } \
                ASpam.nmap.worker.head = (c); \
                (r) = 0; \
            } else { \
                XplSafeIncrement(ASpam.nmap.worker.active); \
                XplSignalBlock(); \
                XplBeginThread(&(id), HandleConnection, 24 * 1024, XPL_UINT_TO_PTR(XplSafeRead(ASpam.nmap.worker.active)), (r)); \
                XplSignalHandler(SignalHandler); \
                if (!(r)) { \
                    (c)->queue.previous = NULL; \
                    if (((c)->queue.next = ASpam.nmap.worker.head) != NULL) { \
                        (c)->queue.next->queue.previous = (c); \
                    } else { \
                        ASpam.nmap.worker.tail = (c); \
                    } \
                    ASpam.nmap.worker.head = (c); \
                } else { \
                    XplSafeDecrement(ASpam.nmap.worker.active); \
                    (r) = -1; \
                } \
            } \
            XplSignalLocalSemaphore(ASpam.nmap.semaphore); \
        }

ASpamGlobals ASpam;

static BOOL 
ASpamClientAllocCB(void *buffer, void *data)
{
    register ASpamClient *c = (ASpamClient *)buffer;

    memset(c, 0, sizeof(ASpamClient));

    return(TRUE);
}

static void 
ASpamClientFree(ASpamClient *client)
{
    register ASpamClient *c = client;

    if (c->conn) {
        ConnClose(c->conn, 1);
        ConnFree(c->conn);
        c->conn = NULL;
    }

    MemPrivatePoolReturnEntry(c);

    return;
}

static void 
FreeClientData(ASpamClient *client)
{
    if (client && !(client->flags & ASPAM_CLIENT_FLAG_EXITING)) {
        client->flags |= ASPAM_CLIENT_FLAG_EXITING;

        if (client->conn) {
            ConnClose(client->conn, 1);
            ConnFree(client->conn);
            client->conn = NULL;
        }

        if (client->envelope) {
            MemFree(client->envelope);
        }
    }

    return;
}

static int 
CmpAddr(const void *c, const void *d)
{
    int r = 0;
    int clen;
    int dlen;
    int ci;
    int di;
    unsigned char cc;
    unsigned char dc;
    const unsigned char *candidate = *(unsigned char**)c;
    const unsigned char *domain = *(unsigned char**)d;

    if (candidate && domain) {
        ci = clen = strlen(candidate) - 1;
        di = dlen = strlen(domain) - 1;

        while ((ci >= 0) && (di >= 0) && (r == 0)) {
            cc = toupper(candidate[ci]);
            dc = toupper(domain[di]);
            if (cc == dc) {
                --ci;
                --di;
            } else if (cc < dc) {
                r = -1;
            } else {
                r = 1;
            }
        }

        if (r == 0) {
            if (clen < dlen) {
                r = -1;
            } else if ((clen > dlen) && (candidate[ci] != '.') && (candidate[ci] != '@')) {
                r = 1;
            } else {
                r = 1;
            }
        }

        return(r);
    }

    return(0);
}

static int 
MatchAddr(unsigned char *candidate, unsigned char *domain)
{
    int r = 0;
    int clen;
    int dlen;
    int ci;
    int di;
    unsigned char cc;
    unsigned char dc;

    if (candidate && domain) {
        ci = clen = strlen(candidate) - 1;
        di = dlen = strlen(domain) - 1;

        while ((ci >= 0) && (di >= 0) && (r == 0)) {
            cc = toupper(candidate[ci]);
            dc = toupper(domain[di]);
            if (cc == dc) {
                --ci;
                --di;
            } else if(cc < dc) {
                r = -1;
            } else {
                r = 1;
            }
        }

        if ((r == 0) && (clen != dlen)) {
            if (clen < dlen) {
                r = -1;
            } else if ((candidate[ci] != '.') && (candidate[ci] != '@')) {
                r = 1;
            }
        }

        return(r);
    }

    return(0);
}

/** Searches for the passed ip address in the disallowed list.
 */
static unsigned char 
*IsSpammer(unsigned char *from)
{
    int cmp;
    int start;
    int end;
    int middle = 0;
    BOOL matched = FALSE;

    start = 0;
    end = ASpam.disallow.used - 1;

    while ((end >= start) && !matched) {
        middle = (end - start) / 2 + start;
        cmp = MatchAddr(from, ASpam.disallow.list->Value[middle]);
        if (cmp == 0) {
            matched = TRUE;
        } else if (cmp < 0) {
            end = middle - 1;
        } else {
            start = middle + 1;
        }
    }

    if(matched) {
        return(ASpam.disallow.list->Value[middle]);
    }

    return(NULL);
}


/** Callback function.  Whenever a new message arrives in the queue that
 * this agent has registered itself on, NMAP calls back to this function
 * and provides the agent with the unique hash of the new message.  The 
 * connection then remains open, and NMAP goes into slave state while
 * the agent does all necessary processing.
 * \param client The connection initiated by the NMAP store.
 */
static __inline int 
ProcessConnection(ASpamClient *client)
{
    int ccode;
    int length;
    unsigned long source = 0;
    unsigned long msgFlags;
    unsigned char *ptr;
    char *ptr2;
    unsigned char *cur;
    unsigned char *line;
    unsigned char *blockedAddr = NULL;
    char *senderUserName = NULL;
    unsigned char qID[16];
    BOOL copy;
    BOOL hasFlags;
    BOOL blocked = FALSE;

    /* Read out the connection answer and determine the size of the message envelope. */
    if (((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) != -1) 
            && (ccode == 6020) 
            && ((ptr = strchr(client->line, ' ')) != NULL)) {
        *ptr++ = '\0';

        strcpy(qID, client->line);

        length = atoi(ptr);
        client->envelope = MemMalloc(length + 3);
    } else {
        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    /* Rename the thread to something useful, and read in the entire envelope. */
    if (client->envelope) {
        sprintf(client->line, "ASpam: %s", qID);
        XplRenameThread(XplGetThreadID(), client->line);

        ccode = NMAPReadCount(client->conn, client->envelope, length);
    } else {
        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    /* Finish the handshake with the NMAP store. */
    if ((ccode != -1) 
            && ((ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, TRUE)) == 6021)) {
        client->envelope[length] = '\0';

        cur = client->envelope;
    } else {
        MemFree(client->envelope);
        client->envelope = NULL;

        NMAPSendCommand(client->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
        return(-1);
    }

    /* NMAP store is now in slave mode. Start processing. */

    ptr = strstr(client->envelope, "\r\n"QUEUES_FLAGS);
    if (ptr) {
        hasFlags = TRUE;
        msgFlags = atol(ptr + 3);
        if (msgFlags & MSG_FLAG_SPAM_CHECKED) {
            /* This message has already been processed */
            MemFree(client->envelope);
            client->envelope = NULL;

            NMAPSendCommand(client->conn, "QDONE\r\n", 7);
            NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);

            return 0;
        }
    } else {
        hasFlags = FALSE;
        msgFlags = 0;
    }

    ptr = strstr(client->envelope, "\r\n"QUEUES_ADDRESS);
    if (ptr) {
        source = atol(ptr + 3);
    } else {
        source = 0;
    }

    if ((ptr = strstr(client->envelope, "\r\n"QUEUES_FROM)) != NULL) {
        char *endLine;
        char *cReturn;
       
        ptr += 3;

        if ((endLine = strchr(ptr, '\n')) != NULL) {
            if (*(endLine - 1) == '\r') {
                cReturn = endLine - 1;
                *cReturn = '\0';
            } else {
                *endLine = '\0';
            }
        }

        if ((ptr = strchr(ptr, ' ')) != NULL) {
            ptr++;
            ptr2 = strchr(ptr , ' ');
            if (ptr2) {
                *ptr2 = '\0';
            }
            if (strcmp(ptr, "-") != 0) {
                senderUserName = MemStrdup(ptr);
            }            
            if (ptr2) {
                *ptr2 = ' ';
            }
        }

        if (endLine) {
            if (cReturn) {
                *cReturn = '\r';
            } else {
                *endLine = '\n';
            }
        }
    }

    if (ASpam.allow.used || ASpam.disallow.used) {
        unsigned char *tmpNull = NULL;
        unsigned char tmpChar;
        while (*cur) { 
            /* Parsing the envelope to see if the sender is blacklisted.
             */
            copy = TRUE;
            line = strchr(cur, 0x0A);
            if (line) {
                if (line[-1] == 0x0D) {
                    tmpNull = line - 1;
                } else {
                    tmpNull = line;
                }

                tmpChar = *tmpNull;
                *tmpNull = '\0';
                line++;
            } else {
                line = cur + strlen(cur);
                tmpChar = '\0';
                tmpNull = NULL;
            }

            switch (cur[0]) {
                case QUEUE_FLAGS: {
                    copy = FALSE;
                    ccode = NMAPSendCommandF(client->conn, "QMOD RAW "QUEUES_FLAGS"%ld\r\n", (msgFlags | MSG_FLAG_SPAM_CHECKED));
                    break;
                }
                case QUEUE_FROM: {
                    ptr = strchr(cur + 1, ' ');
                    if (ptr) {
                        *ptr = '\0';
                        
                        blockedAddr = IsSpammer(cur + 1);
                        if (!blockedAddr) {
                            *ptr = ' ';
                        } else {
                            blocked = TRUE;
                            
                            LoggerEvent(ASpam.handle.logging, LOGGER_SUBSYSTEM_QUEUE, LOGGER_EVENT_SPAM_BLOCKED, LOG_NOTICE, 0, cur + 1, NULL, source, 0, NULL, 0);
                            
                            *ptr = ' ';
                        }
                    }
                    
                    break;
                }
                    
                case QUEUE_RECIP_REMOTE:
                case QUEUE_RECIP_LOCAL:
                case QUEUE_RECIP_MBOX_LOCAL: {
                    if (blocked) {
                        copy = FALSE;
                    }
                    
                    break;
                }
                    
                case QUEUE_ADDRESS: {
                    source = atol(cur + 1);
                    break;
                }
            }
        
            if (copy && (ccode != -1)) {
                ccode = NMAPSendCommandF(client->conn, "QMOD RAW %s\r\n", cur);
            }
            cur = line;
            if (tmpNull) {
                /* Restore the local copy of the envelope. */
                *tmpNull = tmpChar;
            }
        }
    }

    if (!blocked) { 
        blocked = SpamdCheck(&ASpam.spamd, client, qID, hasFlags, msgFlags, source, senderUserName);
    }

    if ((ccode != -1) 
            && ((ccode = NMAPSendCommand(client->conn, "QDONE\r\n", 7)) != -1)) {
        ccode = NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
    }

    if (client->envelope) {
        MemFree(client->envelope);
        client->envelope = NULL;
    }

    if (senderUserName) {
        MemFree(senderUserName);
    }

    return(0);
}

static void 
HandleConnection(void *param)
{
    int ccode;
    long threadNumber = (long)param;
    time_t sleep = time(NULL);
    time_t wokeup;
    ASpamClient *client;

    if ((client = ASpamClientAlloc()) == NULL) {
        XplConsolePrintf("bongoantispam: New worker failed to startup; out of memory.\r\n");

        NMAPSendCommand(client->conn, "QDONE\r\n", 7);

        XplSafeDecrement(ASpam.nmap.worker.active);

        return;
    }

    do {
        XplRenameThread(XplGetThreadID(), "ASpam Worker");

        XplSafeIncrement(ASpam.nmap.worker.idle);

        XplWaitOnLocalSemaphore(ASpam.nmap.worker.todo);

        XplSafeDecrement(ASpam.nmap.worker.idle);

        wokeup = time(NULL);

        XplWaitOnLocalSemaphore(ASpam.nmap.semaphore);

        client->conn = ASpam.nmap.worker.tail;
        if (client->conn) {
            ASpam.nmap.worker.tail = client->conn->queue.previous;
            if (ASpam.nmap.worker.tail) {
                ASpam.nmap.worker.tail->queue.next = NULL;
            } else {
                ASpam.nmap.worker.head = NULL;
            }
        }

        XplSignalLocalSemaphore(ASpam.nmap.semaphore);

        if (client->conn) {
            if (ConnNegotiate(client->conn, ASpam.nmap.ssl.context)) {
                ccode = ProcessConnection(client);
            } else {
                NMAPSendCommand(client->conn, "QDONE\r\n", 7);
                NMAPReadAnswer(client->conn, client->line, CONN_BUFSIZE, FALSE);
            }
        }

        if (client->conn) {
            ConnFlush(client->conn);
        }

        FreeClientData(client);

        /* Live or die? */
        if (threadNumber == XplSafeRead(ASpam.nmap.worker.active)) {
            if ((wokeup - sleep) > ASpam.nmap.sleepTime) {
                break;
            }
        }

        sleep = time(NULL);

        ASpamClientAllocCB(client, NULL);
    } while (ASpam.state == ASPAM_STATE_RUNNING);

    FreeClientData(client);

    ASpamClientFree(client);

    XplSafeDecrement(ASpam.nmap.worker.active);

    XplExitThread(TSR_THREAD, 0);

    return;
}

static void 
AntiSpamServer(void *ignored)
{
    int i;
    int ccode;
    XplThreadID id;
    Connection *conn;

    XplSafeIncrement(ASpam.server.active);

    XplRenameThread(XplGetThreadID(), "Anti-Spam Server");

    while (ASpam.state < ASPAM_STATE_STOPPING) {
        if (ConnAccept(ASpam.nmap.conn, &conn) != -1) {
            if (ASpam.state < ASPAM_STATE_STOPPING) {
                conn->ssl.enable = FALSE;

                QUEUE_WORK_TO_DO(conn, id, ccode);
                if (!ccode) {
                    XplSignalLocalSemaphore(ASpam.nmap.worker.todo);

                    continue;
                }
            }

            ConnWrite(conn, "QDONE\r\n", 7);
            ConnClose(conn, 0);

            ConnFree(conn);
            conn = NULL;

            continue;
        }

        switch (errno) {
            case ECONNABORTED:
#ifdef EPROTO
            case EPROTO: 
#endif
            case EINTR: {
                if (ASpam.state < ASPAM_STATE_STOPPING) {
                    LoggerEvent(ASpam.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);
                }

                continue;
            }

            default: {
                if (ASpam.state < ASPAM_STATE_STOPPING) {
                    XplConsolePrintf("bongoantispam: Exiting after an accept() failure; error %d\r\n", errno);

                    LoggerEvent(ASpam.handle.logging, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);

                    ASpam.state = ASPAM_STATE_STOPPING;
                }

                break;
            }
        }

        break;
    }

    /* Shutting down */
    ASpam.state = ASPAM_STATE_UNLOADING;

#if VERBOSE
    XplConsolePrintf("bongoantispam: Shutting down.\r\n");
#endif

    id = XplSetThreadGroupID(ASpam.id.group);

    if (ASpam.nmap.conn) {
        ConnClose(ASpam.nmap.conn, 1);
        ASpam.nmap.conn = NULL;
    }

    if (ASpam.nmap.ssl.enable) {
        ASpam.nmap.ssl.enable = FALSE;

        if (ASpam.nmap.ssl.conn) {
            ConnClose(ASpam.nmap.ssl.conn, 1);
            ASpam.nmap.ssl.conn = NULL;
        }

        if (ASpam.nmap.ssl.context) {
            ConnSSLContextFree(ASpam.nmap.ssl.context);
            ASpam.nmap.ssl.context = NULL;
        }
    }

    ConnCloseAll(1);

    for (i = 0; (XplSafeRead(ASpam.server.active) > 1) && (i < 60); i++) {
        XplDelay(1000);
    }

#if VERBOSE
    XplConsolePrintf("bongoantispam: Shutting down %d queue threads\r\n", XplSafeRead(ASpam.nmap.worker.active));
#endif

    XplWaitOnLocalSemaphore(ASpam.nmap.semaphore);

    ccode = XplSafeRead(ASpam.nmap.worker.idle);
    while (ccode--) {
        XplSignalLocalSemaphore(ASpam.nmap.worker.todo);
    }

    XplSignalLocalSemaphore(ASpam.nmap.semaphore);

    for (i = 0; XplSafeRead(ASpam.nmap.worker.active) && (i < 60); i++) {
        XplDelay(1000);
    }

    if (XplSafeRead(ASpam.server.active) > 1) {
        XplConsolePrintf("bongoantispam: %d server threads outstanding; attempting forceful unload.\r\n", XplSafeRead(ASpam.server.active) - 1);
    }

    if (XplSafeRead(ASpam.nmap.worker.active)) {
        XplConsolePrintf("bongoantispam: %d threads outstanding; attempting forceful unload.\r\n", XplSafeRead(ASpam.nmap.worker.active));
    }

    SpamdShutdown(&(ASpam.spamd));
    LoggerClose(ASpam.handle.logging);
    ASpam.handle.logging = NULL;

    /* shutdown the scanning engine */

    XplCloseLocalSemaphore(ASpam.nmap.worker.todo);
    XplCloseLocalSemaphore(ASpam.nmap.semaphore);

    if (ASpam.allow.list) {
        MDBDestroyValueStruct(ASpam.allow.list);
        ASpam.allow.list = NULL;
        ASpam.allow.used = 0;
    }

    if (ASpam.disallow.list) {
        MDBDestroyValueStruct(ASpam.disallow.list);
        ASpam.disallow.list = NULL;
        ASpam.disallow.used = 0;
    }

    MsgShutdown();

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();

    MemPrivatePoolFree(ASpam.nmap.pool);

    MemoryManagerClose(MSGSRV_AGENT_ANTISPAM);

#if VERBOSE
    XplConsolePrintf("bongoantispam: Shutdown complete\r\n");
#endif

    XplSignalLocalSemaphore(ASpam.sem.main);
    XplWaitOnLocalSemaphore(ASpam.sem.shutdown);

    XplCloseLocalSemaphore(ASpam.sem.shutdown);
    XplCloseLocalSemaphore(ASpam.sem.main);

    XplSetThreadGroupID(id);

    return;
}

static BOOL 
ReadConfiguration(void) {
    unsigned char *pconfig;
    BOOL retcode = FALSE;
    BongoJsonNode *node;

    if (! NMAPReadConfigFile("antispam", &pconfig)) {
        printf("manager: couldn't read config from store\n");
        return FALSE;
    }

    if (BongoJsonParseString(pconfig, &node) != BONGO_JSON_OK) {
        printf("manager: couldn't parse JSON config\n");
        goto finish;
    }

    SpamdReadConfiguration(&ASpam.spamd, node);

#if 0
    ASpam.allow.list    = MDBCreateValueStruct(ASpam.handle.directory, MsgGetServerDN(NULL));
    ASpam.disallow.list = MDBCreateValueStruct(ASpam.handle.directory, MsgGetServerDN(NULL));
    config              = MDBCreateValueStruct(ASpam.handle.directory, MsgGetServerDN(NULL));
    if (ASpam.allow.list && ASpam.disallow.list && config) {
        ASpam.allow.used = 0;
        ASpam.disallow.used = 0;
    } else {
        if (config) {
            MDBDestroyValueStruct(config);
        }

        retcode = FALSE;
        goto finish;
    }
#endif

    retcode = TRUE;
finish:
    BongoJsonNodeFree(node);
    return retcode;
#if 0
    unsigned char *ptr;

    /* Find out which queue to register with.  Otherwise remain Q_INCOMING */
    if (MDBRead(MSGSRV_AGENT_ANTISPAM, MSGSRV_A_REGISTER_QUEUE, config) > 0) {
	ASpam.nmap.queue = atol(config->Value[0]);
    }
    MDBFreeValues(config);

    if ((ASpam.disallow.used = MDBRead(MSGSRV_AGENT_ANTISPAM, MSGSRV_A_EMAIL_ADDRESS, ASpam.disallow.list)) > 0) {
        qsort(ASpam.disallow.list->Value, ASpam.disallow.used, sizeof(unsigned char*), CmpAddr);

        MDBFreeValues(config);
    }

    MDBSetValueStructContext(NULL, config);
    if (MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, config)>0) { 
        HashCredential(MsgGetServerDN(NULL), config->Value[0], ASpam.nmap.hash);
    }

    MDBDestroyValueStruct(config);

    return(TRUE);
#endif
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
static int 
_NonAppCheckUnload(void)
{
    static BOOL    checked = FALSE;
    XplThreadID    id;

    if (!checked) {
        checked = TRUE;
        ASpam.state = ASPAM_STATE_UNLOADING;

        XplWaitOnLocalSemaphore(ASpam.sem.shutdown);

        id = XplSetThreadGroupID(ASpam.id.group);
        ConnClose(ASpam.nmap.conn, 1);
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(ASpam.sem.main);
    }

    return(0);
}
#endif

static void 
SignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (ASpam.state < ASPAM_STATE_UNLOADING) {
                ASpam.state = ASPAM_STATE_UNLOADING;
            }

            break;
        }

        case SIGINT:
        case SIGTERM: {
            if (ASpam.state == ASPAM_STATE_STOPPING) {
                XplUnloadApp(getpid());
            } else if (ASpam.state < ASPAM_STATE_STOPPING) {
                ASpam.state = ASPAM_STATE_STOPPING;
            }

            break;
        }

        default: {
            break;
        }
    }

    return;
}

static int 
QueueSocketInit(void)
{
    ASpam.nmap.conn = ConnAlloc(FALSE);
    if (ASpam.nmap.conn) {
        memset(&(ASpam.nmap.conn->socketAddress), 0, sizeof(ASpam.nmap.conn->socketAddress));

        ASpam.nmap.conn->socketAddress.sin_family = AF_INET;
        ASpam.nmap.conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();

        /* Get root privs back for the bind.  It's ok if this fails -
        * the user might not need to be root to bind to the port */
        XplSetEffectiveUserId(0);

        ASpam.nmap.conn->socket = ConnServerSocket(ASpam.nmap.conn, 2048);
        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            XplConsolePrintf("bongoantispam: Could not drop to unprivileged user '%s'\r\n", MsgGetUnprivilegedUser());
            ConnFree(ASpam.nmap.conn);
            ASpam.nmap.conn = NULL;
            return(-1);
        }

        if (ASpam.nmap.conn->socket == -1) {
            XplConsolePrintf("bongoantispam: Could not bind to dynamic port\r\n");
            ConnFree(ASpam.nmap.conn);
            ASpam.nmap.conn = NULL;
            return(-1);
        }

        if (NMAPRegister(MSGSRV_AGENT_ANTISPAM, ASpam.nmap.queue, ASpam.nmap.conn->socketAddress.sin_port) != REGISTRATION_COMPLETED) {
            XplConsolePrintf("bongoantispam: Could not register with bongonmap\r\n");
            ConnFree(ASpam.nmap.conn);
            ASpam.nmap.conn = NULL;
            return(-1);
        }
    } else {
        XplConsolePrintf("bongoantispam: Could not allocate connection.\r\n");
        return(-1);
    }

    return(0);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int                ccode;

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("bongoantispam: Could not drop to unprivileged user '%s', exiting.\n", MsgGetUnprivilegedUser());
        return(1);
    }
    XplInit();

    XplSignalHandler(SignalHandler);

    ASpam.id.main = XplGetThreadID();
    ASpam.id.group = XplGetThreadGroupID();

    ASpam.state = ASPAM_STATE_INITIALIZING;
    ASpam.flags = 0;

    ASpam.nmap.conn = NULL;
    ASpam.nmap.queue = Q_INCOMING;
    ASpam.nmap.pool = NULL;
    ASpam.nmap.sleepTime = (5 * 60);
    ASpam.nmap.ssl.conn = NULL;
    ASpam.nmap.ssl.enable = FALSE;
    ASpam.nmap.ssl.context = NULL;
    ASpam.nmap.ssl.config.options = 0;
 
    ASpam.handle.directory = NULL;
    ASpam.handle.logging = NULL;

    strcpy(ASpam.nmap.address, "127.0.0.1");

    XplSafeWrite(ASpam.server.active, 0);

    XplSafeWrite(ASpam.nmap.worker.idle, 0);
    XplSafeWrite(ASpam.nmap.worker.active, 0);
    XplSafeWrite(ASpam.nmap.worker.maximum, 100000);

    if (MemoryManagerOpen(MSGSRV_AGENT_ANTISPAM) == TRUE) {
        ASpam.nmap.pool = MemPrivatePoolAlloc("AntiSpam Connections", sizeof(ASpamClient), 0, 3072, TRUE, FALSE, ASpamClientAllocCB, NULL, NULL);
        if (ASpam.nmap.pool != NULL) {
            XplOpenLocalSemaphore(ASpam.sem.main, 0);
            XplOpenLocalSemaphore(ASpam.sem.shutdown, 1);
            XplOpenLocalSemaphore(ASpam.nmap.semaphore, 1);
            XplOpenLocalSemaphore(ASpam.nmap.worker.todo, 1);
        } else {
            MemoryManagerClose(MSGSRV_AGENT_ANTISPAM);

            XplConsolePrintf("bongoantispam: Unable to create connection pool; shutting down.\r\n");
            return(-1);
        }
    } else {
        XplConsolePrintf("bongoantispam: Unable to initialize memory manager; shutting down.\r\n");
        return(-1);
    }

    ConnStartup(CONNECTION_TIMEOUT, TRUE);

    MDBInit();
    ASpam.handle.directory = (MDBHandle)MsgInit();
    if (ASpam.handle.directory == NULL) {
        XplBell();
        XplConsolePrintf("bongoantispam: Invalid directory credentials; exiting!\r\n");
        XplBell();

        MemoryManagerClose(MSGSRV_AGENT_ANTISPAM);

        return(-1);
    }

    NMAPInitialize(ASpam.handle.directory);

    SetCurrentNameSpace(NWOS2_NAME_SPACE);
    SetTargetNameSpace(NWOS2_NAME_SPACE);

    ASpam.handle.logging = LoggerOpen("bongoantispam");
    if (!ASpam.handle.logging) {
            XplConsolePrintf("bongoantispam: Unable to initialize logging; disabled.\r\n");
    }

    ReadConfiguration();
    CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), "antispam");
    // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */

    SpamdStartup(&(ASpam.spamd));

    // TODO: *.used variables seem to be about spam IP sources. This doesn't seem to be
    // the right place, really...
    // if (ASpam.allow.used || ASpam.disallow.used) {
        if (QueueSocketInit() < 0) {
            XplConsolePrintf("bongoantispam: Exiting.\r\n");

            MemoryManagerClose(MSGSRV_AGENT_ANTISPAM);

            return -1;
        }

        /* initialize scanning engine here */

        if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
            XplConsolePrintf("bongoantispam: Could not drop to unprivileged user '%s', exiting.\r\n", MsgGetUnprivilegedUser());

            MemoryManagerClose(MSGSRV_AGENT_ANTISPAM);

            return 1;
        }

        ASpam.nmap.ssl.enable = FALSE;
        ASpam.nmap.ssl.config.certificate.file = MsgGetTLSCertPath(NULL);
        ASpam.nmap.ssl.config.key.type = GNUTLS_X509_FMT_PEM;
        ASpam.nmap.ssl.config.key.file = MsgGetTLSKeyPath(NULL);

        ASpam.nmap.ssl.context = ConnSSLContextAlloc(&(ASpam.nmap.ssl.config));
        if (ASpam.nmap.ssl.context) {
            ASpam.nmap.ssl.enable = TRUE;
        }

        NMAPSetEncryption(ASpam.nmap.ssl.context);

        ASpam.state = ASPAM_STATE_RUNNING;
#if 0
    } else {
        XplConsolePrintf("bongoantispam: no hosts allowed or disallowed; unloading\r\n");
        
        ASpam.state = ASPAM_STATE_STOPPING;        
    }
#endif

    XplStartMainThread(PRODUCT_SHORT_NAME, &id, AntiSpamServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    return(0);
}
