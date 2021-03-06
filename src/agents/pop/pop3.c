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

/* Compile-time options */
#undef USE_HOPCOUNT_DETECTION

#define LOGGERNAME "pop3d"
#define PRODUCT_NAME "Bongo POP3 Agent"
#define PRODUCT_VERSION "$Revision: 1.5 $"

#include <xpl.h>
#include <bongoutil.h>
#include <connio.h>
#include <msgapi.h>
#include <logger.h>
#include <nmlib.h>
#include <nmap.h>
#include <bongostore.h>

#include <bongoagent.h>

#include "pop3.h"

#undef NO_READONLY_MBOX

/* Minutes * 60 (1 second granularity) */
#define CONNECTION_TIMEOUT (15 * 60)

#define MAX_LOGIN_FAILURES 3

#define PIPELINE_LIMIT 100
#define POP_STACK_SPACE (1024 * 32)

#define POP3_COMMAND_APOP "APOP"
#define POP3_COMMAND_AUTH "AUTH"
#define POP3_COMMAND_CAPA "CAPA"
#define POP3_COMMAND_DELE "DELE"
#define POP3_COMMAND_GURL "GURL"
#define POP3_COMMAND_LAST "LAST"
#define POP3_COMMAND_LIST "LIST"
#define POP3_COMMAND_NOOP "NOOP"
#define POP3_COMMAND_PASS "PASS"
#define POP3_COMMAND_QUIT "QUIT"
#define POP3_COMMAND_RETR "RETR"
#define POP3_COMMAND_RSET "RSET"
#define POP3_COMMAND_STARTTLS "STARTTLS"
#define POP3_COMMAND_STAT "STAT"
#define POP3_COMMAND_STLS "STLS"
#define POP3_COMMAND_TOP "TOP"
#define POP3_COMMAND_USER "USER"
#define POP3_COMMAND_UIDL "UIDL"
#define POP3_COMMAND_XAUTHLIST "XAUTHLIST"
#define POP3_COMMAND_XSENDER "XSENDER"
#define POP3_COMMAND_XTND "XTND"

/* Response codes for ConnectUserToNMAPServer */
#define POP3_NMAP_SERVER_DOWN -2
#define POP3_NMAP_USER_UNKNOWN -3
#define POP3_NMAP_PASSWORD_INVALID -4
#define POP3_NMAP_FEATURE_DISABLED -5

#define GetPOP3ClientPoolEntry() MemPrivatePoolGetEntryDirect(POP3.client.pool, __FILE__, __LINE__)

static void SignalHandler(int sigtype);

#define QUEUE_WORK_TO_DO(c, id, r) \
        { \
            XplWaitOnLocalSemaphore(POP3.client.semaphore); \
            if (XplSafeRead(POP3.client.worker.idle)) { \
                (c)->queue.previous = NULL; \
                if (((c)->queue.next = POP3.client.worker.head) != NULL) { \
                    (c)->queue.next->queue.previous = (c); \
                } else { \
                    POP3.client.worker.tail = (c); \
                } \
                POP3.client.worker.head = (c); \
                (r) = 0; \
            } else if (XplSafeRead(POP3.client.worker.active) < XplSafeRead(POP3.client.worker.maximum)) { \
                XplSafeIncrement(POP3.client.worker.active); \
                XplSignalBlock(); \
                XplBeginThread(&(id), HandleConnection, POP_STACK_SPACE, (char *)(XplSafeRead(POP3.client.worker.active)), (r)); \
                XplSignalHandler(SignalHandler); \
                if (!(r)) { \
                    (c)->queue.previous = NULL; \
                    if (((c)->queue.next = POP3.client.worker.head) != NULL) { \
                        (c)->queue.next->queue.previous = (c); \
                    } else { \
                        POP3.client.worker.tail = (c); \
                    } \
                    POP3.client.worker.head = (c); \
                } else { \
                    XplSafeDecrement(POP3.client.worker.active); \
                    (r) = POP3_RECEIVER_OUT_OF_MEMORY; \
                } \
            } else { \
                (r) = POP3_RECEIVER_CONNECTION_LIMIT; \
            } \
            XplSignalLocalSemaphore(POP3.client.semaphore); \
        }

enum POP3States {
    POP3_STARTING = 0, 
    POP3_INITIALIZING, 
    POP3_LOADING, 
    POP3_RUNNING, 
    POP3_RELOADING, 
    POP3_UNLOADING, 
    POP3_STOPPING, 
    POP3_DONE, 

    POP3_MAX_STATES
};

enum POP3ReceiverStates {
    POP3_RECEIVER_SHUTTING_DOWN = 1, 
    POP3_RECEIVER_DISABLED, 
    POP3_RECEIVER_CONNECTION_LIMIT, 
    POP3_RECEIVER_OUT_OF_MEMORY, 

    POP3_RECEIVER_MAX_STATES
};

enum POP3ClientStates {
    POP3_CLIENT_FRESH = 0, 
    POP3_CLIENT_AUTHORIZATION, 
    POP3_CLIENT_TRANSACTION, 
    POP3_CLIENT_UPDATE, 
    POP3_CLIENT_ENDING, 

    POP3_CLIENT_MAX_STATES
};

typedef struct {
    uint64_t        guid;
    unsigned long   size;
    BOOL            deleted;
} MessageInformation;

typedef struct _POP3Client {
    enum POP3ClientStates state;

    BOOL readOnly;

    Connection  *conn;
    Connection  *store;

    ProtocolCommand *handler;

    unsigned char user[MAXEMAILNAMESIZE + 1];

    unsigned char command[CONN_BUFSIZE + 1];
    unsigned char buffer[CONN_BUFSIZE + 1];

    unsigned long messageCount;
    unsigned long messagesDeleted;

    unsigned long storeSize;
    unsigned long storeSizeDeleted;

    MessageInformation  *message;

    int loginCount;
} POP3Client;

struct {
    enum POP3States state;

    BOOL stopped;

    struct {
        XplThreadID main;
        XplThreadID group;
    } id;

    struct {
        XplSemaphore main;
        XplSemaphore shutdown;
    } sem;

    struct {
        struct {
            BOOL enable;

            ConnSSLConfiguration config;

            bongo_ssl_context *context;

            Connection *conn;
        } ssl;

        Connection *conn;

        XplAtomic active;

        struct sockaddr_in addr;
    } server;

    struct {
        XplSemaphore semaphore;

        struct {
            XplSemaphore todo;

            XplAtomic maximum;
            XplAtomic active;
            XplAtomic idle;

            Connection *head;
            Connection *tail;
        } worker;

        void *pool;

        time_t sleepTime;
    } client;

    struct {
        struct {
            BOOL enable;

            ConnSSLConfiguration config;

            bongo_ssl_context *context;

            Connection *conn;
        } ssl;

        unsigned char address[80];
        unsigned char hash[NMAP_HASH_SIZE];
    } nmap;

    struct {
        struct {
            XplAtomic serviced;
        } clients;

        XplAtomic badPasswords;
    } stats;

    unsigned char managementURL[256];

    void *loggingHandle;

    ProtocolCommandTree commands;
} POP3;

static BOOL HandleConnection(void *param);

static int POP3CommandAPOP(void *param);
static int POP3CommandAuth(void *param);
static int POP3CommandCapa(void *param);
static int POP3CommandDele(void *param);
static int POP3CommandGURL(void *param);
static int POP3CommandLast(void *param);
static int POP3CommandList(void *param);
static int POP3CommandNoop(void *param);
static int POP3CommandPass(void *param);
static int POP3CommandQuit(void *param);
static int POP3CommandRetr(void *param);
static int POP3CommandRSet(void *param);
static int POP3CommandStat(void *param);
static int POP3CommandSTLS(void *param);
static int POP3CommandTop(void *param);
static int POP3CommandUser(void *param);
static int POP3CommandUIDL(void *param);
static int POP3CommandXTND(void *param);

static ProtocolCommand POP3CommandEntries[] = {
    { POP3_COMMAND_APOP, NULL, sizeof(POP3_COMMAND_APOP) - 1, POP3CommandAPOP, NULL, NULL }, 
    { POP3_COMMAND_AUTH, NULL, sizeof(POP3_COMMAND_AUTH) - 1, POP3CommandAuth, NULL, NULL }, 
    { POP3_COMMAND_CAPA, NULL, sizeof(POP3_COMMAND_CAPA) - 1, POP3CommandCapa, NULL, NULL }, 
    { POP3_COMMAND_DELE, NULL, sizeof(POP3_COMMAND_DELE) - 1, POP3CommandDele, NULL, NULL }, 
    { POP3_COMMAND_GURL, NULL, sizeof(POP3_COMMAND_GURL) - 1, POP3CommandGURL, NULL, NULL }, 
    { POP3_COMMAND_LAST, NULL, sizeof(POP3_COMMAND_LAST) - 1, POP3CommandLast, NULL, NULL }, 
    { POP3_COMMAND_LIST, NULL, sizeof(POP3_COMMAND_LIST) - 1, POP3CommandList, NULL, NULL }, 
    { POP3_COMMAND_NOOP, NULL, sizeof(POP3_COMMAND_NOOP) - 1, POP3CommandNoop, NULL, NULL }, 
    { POP3_COMMAND_PASS, NULL, sizeof(POP3_COMMAND_PASS) - 1, POP3CommandPass, NULL, NULL }, 
    { POP3_COMMAND_QUIT, NULL, sizeof(POP3_COMMAND_QUIT) - 1, POP3CommandQuit, NULL, NULL }, 
    { POP3_COMMAND_RETR, NULL, sizeof(POP3_COMMAND_RETR) - 1, POP3CommandRetr, NULL, NULL }, 
    { POP3_COMMAND_RSET, NULL, sizeof(POP3_COMMAND_RSET) - 1, POP3CommandRSet, NULL, NULL }, 
    { POP3_COMMAND_STARTTLS, NULL, sizeof(POP3_COMMAND_STARTTLS) - 1, POP3CommandSTLS, NULL, NULL }, 
    { POP3_COMMAND_STAT, NULL, sizeof(POP3_COMMAND_STAT) - 1, POP3CommandStat, NULL, NULL }, 
    { POP3_COMMAND_STLS, NULL, sizeof(POP3_COMMAND_STLS) - 1, POP3CommandSTLS, NULL, NULL }, 
    { POP3_COMMAND_TOP, NULL, sizeof(POP3_COMMAND_TOP) - 1, POP3CommandTop, NULL, NULL }, 
    { POP3_COMMAND_USER, NULL, sizeof(POP3_COMMAND_USER) - 1, POP3CommandUser, NULL, NULL }, 
    { POP3_COMMAND_UIDL, NULL, sizeof(POP3_COMMAND_UIDL) - 1, POP3CommandUIDL, NULL, NULL }, 
    { POP3_COMMAND_XTND, NULL, sizeof(POP3_COMMAND_XTND) - 1, POP3CommandXTND, NULL, NULL }, 

    { NULL, NULL, 0, NULL, NULL, NULL }
};

unsigned long  POP3ServerPort = POP3_PORT;
unsigned long  POP3ServerPortSSL = POP3_PORT_SSL;

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
int 
_NonAppCheckUnload(void)
{
    int            s;
    static BOOL    checked = FALSE;
    XplThreadID    id;

    if (!checked) {
        checked = POP3.state = POP3_UNLOADING;

        XplWaitOnLocalSemaphore(POP3.sem.shutdown);

        id = XplSetThreadGroupID(POP3.id.group);
        if (POP3.server.conn->socket != -1) {
            s = POP3.server.conn->socket;
            POP3.server.conn->socket = -1;

            IPclose(s);
        }
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(POP3.sem.main);
    }

    return(0);
}
#endif

static void 
SignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (POP3.state < POP3_UNLOADING) {
                POP3.state = POP3_UNLOADING;
            }

            break;
        }
        case SIGINT :
        case SIGTERM: {
            if (POP3.state == POP3_STOPPING) {
                XplUnloadApp(getpid());
            } else if (POP3.state < POP3_STOPPING) {
                POP3.state = POP3_STOPPING;
            }

            break;
        }

        default: {
            break;
        }
    }

    return;
}

#if 0
// FIXME: Function deprecated, or just waiting for a new signal framework
static BOOL 
POP3Shutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    int s;
    XplThreadID id;

    if (response) {
        if (!arguments) {
            if (POP3.server.conn && (POP3.server.conn->socket != -1)) {
                *response = MemStrdup("Shutting down.\r\n");
                if (*response) {
                    id = XplSetThreadGroupID(POP3.id.group);

                    POP3.state = POP3_STOPPING;

                    s = POP3.server.conn->socket;
                    POP3.server.conn->socket = -1;

                    if (s != -1) {
                        IPshutdown(s, 2);
                        IPclose(s);
                    }

                    if (closeConnection) {
                        *closeConnection = TRUE;
                    }

                    XplSetThreadGroupID(id);
                }
            } else if (POP3.state >= POP3_STOPPING) {
                *response = MemStrdup("Shutdown in progress.\r\n");
            } else {
                *response = MemStrdup("Unknown shutdown state.\r\n");
            }

            if (*response) {
                return(TRUE);
            }

            return(FALSE);
        }

        *response = MemStrdup("arguments not allowed.\r\n");
        return(TRUE);
    }

    return(FALSE);
}
#endif

static BOOL 
POP3ClientAllocCB(void *buffer, void *clientData)
{
    POP3Client *c = (POP3Client *)buffer;

    memset(c, 0, sizeof(POP3Client));

    c->state = POP3_CLIENT_FRESH;

    return(TRUE);
}

static void 
POP3ReturnClientPoolEntry(POP3Client *client)
{
    register POP3Client *c = client;

    memset(c, 0, sizeof(POP3Client));

    c->state = POP3_CLIENT_FRESH;

    MemPrivatePoolReturnEntry(POP3.client.pool, c);

    return;
}

static int 
ConnectUserToNMAPServer(POP3Client *client, unsigned char *username, unsigned char *password)
{
    BOOL result;
    struct sockaddr_in nmap;

    if (MsgAuthFindUser(username) != 0) {
        Log(LOG_NOTICE, "Unknown user %s from host %s", username, LOGIP(client->conn->socketAddress));
        return(POP3_NMAP_USER_UNKNOWN);
    }

    if (MsgAuthVerifyPassword(username, password) != 0) {
        XplSafeIncrement(POP3.stats.badPasswords);
        Log(LOG_NOTICE, "Incorrect password for user %s from host %s", username,
            LOGIP(client->conn->socketAddress));
        /* the use was not found.  Error, don't continue */
        return(POP3_NMAP_USER_UNKNOWN);
    }

    MsgAuthGetUserStore(username, &nmap);
    if ((client->store = NMAPConnectEx("127.0.0.1", NULL, client->conn->trace.destination)) != NULL) {
        result = NMAPAuthenticateToStore(client->store, client->buffer, CONN_BUFSIZE);
    } else {
        Log(LOG_ERROR, "Cannot connect to Store Agent on host %s", LOGIP(nmap));
        return(POP3_NMAP_SERVER_DOWN);
    }

    if (result) {
        return(0);
    }

    NMAPQuit(client->store);
    ConnFree(client->store);
    client->store = NULL;

    return(POP3_NMAP_SERVER_DOWN);
}

static BOOL
LoadIDList(POP3Client *client)
{
    int ccode;
    int result;
    unsigned long allocCount;
    unsigned int  flags, type;
    unsigned long size;
    uint64_t      guid;
    MessageInformation *tmpList;

    ccode = NMAPSendCommand(client->store, "LIST /mail/INBOX\r\n", 18);
    if (ccode == -1) {
        ConnWriteF(client->conn, "-ERR [SYS/TEMP] NMAP Failure:%d\r\n", ccode);
        ConnFlush(client->conn);
        return(FALSE);
    }

    if (client->message != NULL) {
        MemFree(client->message);
    }

    client->messageCount = 0;
    client->messagesDeleted = 0;
    client->storeSize = 0;
    client->storeSizeDeleted = 0;
    allocCount = 100;
    client->message = MemMalloc(sizeof(MessageInformation) * allocCount);
    memset(client->message, 0, sizeof(MessageInformation) * allocCount);


    do {
        ccode = NMAPReadAnswer(client->store, client->buffer, CONN_BUFSIZE, FALSE);
        switch(ccode)
        {
            case 2001:
                if (!client->message) break;    /* Alloc failed - read into bit bucket */

                if(client->messageCount == allocCount)
                {
                    tmpList = MemRealloc(client->message, sizeof(MessageInformation) * allocCount * 2);
                    if (tmpList) {
                        client->message = tmpList;
                        memset(&client->message[client->messageCount], 0, sizeof(MessageInformation) * allocCount);
                        allocCount *= 2;
                    }
                    else {
                        MemFree(client->message);
                        client->message = NULL;
                        break;
                    }
                }

                result = sscanf(client->buffer, "%*u %llx %u %u %*x %*u %lu %*s", &guid, &type, &flags, &size);
                if(result != 4)
                {   /* sscanf failed - must have been passed a bad LIST string - bail out! */
                    ccode = -1;
                    break;
                }
                if (!(flags & STORE_MSG_FLAG_DELETED) && (type == 2)) {
                    client->message[client->messageCount].guid = guid;
                    client->message[client->messageCount].size = size;
                    client->messageCount++;
                    client->storeSize += size;
                }
                break;

            case 1000:
                break;

            default:
                break;
        }
    } while(ccode == 2001);

    if(ccode == 1000)
    {
        if(client->message) {
//            qsort(folder->message, folder->messageCount, sizeof(MessageInformation), CompareMessageInformation);
            return(TRUE);
        }
        else {
            ConnWrite(client->conn, MSGERRNOMEMORY, sizeof(MSGERRNOMEMORY));
            ConnFlush(client->conn);
            return(FALSE);
        }
    }

    ConnWriteF(client->conn, "-ERR [SYS/TEMP] NMAP Failure:%d\r\n", ccode);
    ConnFlush(client->conn);

    if(client->message) {
        MemFree(client->message);
        client->message = NULL;
    }

    return(FALSE);
}

static int 
POP3CommandAPOP(void *param)
{
    POP3Client *client = (POP3Client *)param;

    return(ConnWrite(client->conn, MSGERRNOTSUPPORTED, sizeof(MSGERRNOTSUPPORTED) - 1));
}

static int 
POP3CommandAuth(void *param)
{
    POP3Client *client = (POP3Client *)param;

    return(ConnWrite(client->conn, MSGERRNOTSUPPORTED, sizeof(MSGERRNOTSUPPORTED) - 1));
}

#if 0
static int 
POP3CommandAuth(void *param)
{
    int ccode;
    POP3Client *client = (POP3Client *)param;

    /* fixme - using fixed salts? */
    if (client->state == POP3_CLIENT_FRESH) {
        if (client->command[4] != ' ') {
            return(ConnWrite(client->conn, MSGERRSYNTAX, sizeof(MSGERRSYNTAX) - 1));
        }

        if (XplStrNCaseCmp(client->command + 5, "LOGIN", 5) == 0) {
            ccode = ConnWrite(client->conn, "+ VXNlcm5hbWU6AA==\r\n", 20);
        } else {
            return(ConnWrite(client->conn, MSGWRONGMETHOD, sizeof(MSGWRONGMETHOD) - 1));
        }
    } else {
        return(ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1));
    }

    do
    {
        ccode = ConnFlush(client->conn);
        if (ccode == -1) break;
    
        ccode = ConnReadAnswer(client->conn, client->buffer, CONN_BUFSIZE);
        if (ccode == -1) break;
    
        /* fixme - using static salts? */
        DecodeBase64(client->buffer);
    
        ccode = ConnWrite(client->conn, "+ UGFzc3dvcmQ6AA==\r\n", 20);
        if (ccode == -1) break;
    
        ccode = ConnFlush(client->conn);
        if (ccode == -1) break;
    
        ccode = ConnReadAnswer(client->conn, client->command, CONN_BUFSIZE);
        if (ccode == -1) break;
    
        DecodeBase64(client->command);
    
        strcpy(client->user, client->buffer);
    
        ccode = ConnectUserToNMAPServer(client, client->user, client->command);
        if (!ccode) {
            LoggerEvent(POP3.loggingHandle, LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_LOGIN, LOG_INFO, 0, client->user, NULL, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
    
        } else {
            if (ccode == POP3_NMAP_SERVER_DOWN) {
                ConnWriteF(client->conn, "-ERR [SYS/PERM] %s %s\r\n", POP3.hostName, MSGERRNONMAP);
            } else if (ccode != POP3_NMAP_FEATURE_DISABLED) {
                XplDelay(2000);
                client->loginCount++;
                if (client->loginCount < MAX_LOGIN_FAILURES) {
                    ConnWrite(client->conn, MSGERRNOPERMISSION, sizeof(MSGERRNOPERMISSION) - 1);
                    return(0);
                }
                ConnWrite(client->conn, MSGERRNOPERMISSIONCLOSING, sizeof(MSGERRNOPERMISSIONCLOSING) - 1);
            } else {
                ConnWrite(client->conn, MSGERRNORIGHTS, sizeof(MSGERRNORIGHTS) - 1);
            }
    
            break;
        }
    
        ccode = NMAPSendCommandF(client->store, "USER %s\r\n", client->user);
        if (ccode == -1) break;
    
        ccode = NMAPReadAnswer(client->store, client->buffer, CONN_BUFSIZE, TRUE);
        switch(ccode) {
            case 3010: 
            case 3241: {
                ConnWrite(client->conn, MSGERRNOBOX, sizeof(MSGERRNOBOX) - 1);
                break;
            }
    
            case 1000: {
                ccode = 0;
                break;
            }
    
            default: {
                ConnWriteF(client->conn, "-ERR [SYS/PERM] %s %s\r\n", POP3.hostName, MSGERRNONMAP);
                break;
            }
        }
        if (ccode != 0) break;
    
        ccode = NMAPSendCommandF(client->store, "STORE %s\r\n", client->user);
        if (ccode == -1) break;
    
        ccode = NMAPReadAnswer(client->store, client->buffer, CONN_BUFSIZE, TRUE);
        switch(ccode) {
            case 3010: 
            case 3241: {
                ConnWrite(client->conn, MSGERRNOBOX, sizeof(MSGERRNOBOX) - 1);
                break;
            }
    
            case 1000: {
                ccode = 0;
                break;
            }
    
            default: {
                ConnWriteF(client->conn, "-ERR [SYS/PERM] %s %s\r\n", POP3.hostName, MSGERRNONMAP);
                break;
            }
        }
        if (ccode != 0) break;
    
        if (!LoadIDList(client)) {
            ccode = -1;
            break;
        }
        ccode = 0;
    } while (0);

    if (ccode == 0) {
        client->state = POP3_CLIENT_TRANSACTION;
        ConnWrite(client->conn, MSGOK, sizeof(MSGOK) - 1);

        return(ccode);
    }
    else {
        /* fixme - we need connection management flags for ConnClose to force the shutdown */
        ConnClose(client->conn);
        ConnFree(client->conn);
        client->conn = NULL;
        return(ccode);
    }
}
#endif

static int 
POP3CommandCapa(void *param)
{
    int ccode;
    POP3Client *client = (POP3Client *)param;

    if (POP3.server.ssl.enable) {
        ccode = ConnWrite(client->conn, MSGCAPALIST, sizeof(MSGCAPALIST) - 1);
    } else {
        ccode = ConnWrite(client->conn, MSGCAPALIST_NOTLS, sizeof(MSGCAPALIST_NOTLS) - 1);
    }

    return(ccode);
}

static int 
POP3CommandDele(void *param)
{
    int ccode;
    unsigned long id;
    POP3Client *client = (POP3Client *)param;

    if (client->state != POP3_CLIENT_TRANSACTION) {
        return(ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1));
    }

    if (client->command[4] != ' ') {
        return(ConnWrite(client->conn, MSGERRSYNTAX, sizeof(MSGERRSYNTAX) - 1));
    }

    if (client->readOnly) {
        return(ConnWrite(client->conn, MSGERRREADONLY, sizeof(MSGERRREADONLY) - 1));
    }

    id = atoi(client->command + 5);
    if ((id <= 0) || (id > client->messageCount) || client->message[--id].deleted) {
        return(ConnWrite(client->conn, MSGERRNOMSG, sizeof(MSGERRNOMSG) - 1));
    }

    client->message[id].deleted = TRUE;	// Mark the message as deleted
    client->storeSizeDeleted += client->message[id].size;
    client->messagesDeleted++;

    ccode = ConnWrite(client->conn, MSGOK, sizeof(MSGOK) - 1);
/*
    ccode = NMAPSendCommandF(client->store, "DELE %ld\r\n", client->idList[id]);

    if (ccode != -1) {
        ccode = NMAPReadAnswer(client->store, client->buffer, CONN_BUFSIZE, TRUE);
        switch(ccode) {
            case 1000: {
                ccode = ConnWrite(client->conn, MSGOK, sizeof(MSGOK) - 1);
                break;
            }

            case 4220: {
                ccode = ConnWrite(client->conn, MSGERRNOMSG, sizeof(MSGERRNOMSG) - 1);
                break;
            }

            default: {
                ccode = ConnWriteF(client->conn, "%s %d\r\n", MSGERRNMAP, ccode);
                break;
            }
        }
    }
*/
    return(ccode);
}

static int 
POP3CommandGURL(void *param)
{
    int ccode;
    POP3Client *client = (POP3Client *)param;

    /* Get account administration URL
    fixme - we don't have that nds entry yet */

    return(ccode = ConnWriteF(client->conn, "+OK %s\r\n", POP3.managementURL));
}

static int 
POP3CommandLast(void *param)
{
    POP3Client *client = (POP3Client *)param;

    if (client->state != POP3_CLIENT_TRANSACTION) {
        return(ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1));
    }

	 return(ConnWrite(client->conn, MSGERRNOTSUPPORTED, sizeof(MSGERRNOTSUPPORTED) - 1));
}

static int 
POP3CommandList(void *param)
{
    int ccode;
    unsigned long id;
    POP3Client *client = (POP3Client *)param;

    if (client->state != POP3_CLIENT_TRANSACTION) {
        return(ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1));
    }

    if (client->command[4] == ' ') {
        /* for a specific message */
        id = atoi(client->command + 5);;

        if ((id <= 0) || (id > client->messageCount) || client->message[--id].deleted) {
            return(ConnWrite(client->conn, MSGERRNOMSG, sizeof(MSGERRNOMSG) - 1));
        }

        ccode = ConnWriteF(client->conn, "+OK %ld %lu\r\n", id + 1, client->message[id].size);

        return(ccode);
    }

    ccode = ConnWrite(client->conn, MSGOK, sizeof(MSGOK) - 1);

    if (ccode != -1) {
        for(id = 0; id < client->messageCount; id++) {
            if(client->message[id].deleted) {
                continue;
            }
            ccode = ConnWriteF(client->conn, "%lu %lu\r\n", id + 1, client->message[id].size);
        }
        ccode = ConnWrite(client->conn, ".\r\n", 3);
    }

    return(ccode);
}

static int 
POP3CommandNoop(void *param)
{
    int ccode;
    POP3Client *client = (POP3Client *)param;

    if (client->state == POP3_CLIENT_TRANSACTION) {
        ccode = ConnWrite(client->conn, MSGOK, sizeof(MSGOK) - 1);
    } else {
        ccode = ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1);
    }

    return(ccode);
}

static int 
POP3CommandPass(void *param)
{
    int ccode;
    unsigned char *ptr;
    POP3Client *client = (POP3Client *)param;

    if (client->state == POP3_CLIENT_AUTHORIZATION) {
        if (client->command[4] == ' ') {
            ptr = client->command + 5;
            while (isspace(*ptr)) {
                ptr++;
            }
        } else {
            return(ConnWrite(client->conn, MSGERRSYNTAX, sizeof(MSGERRSYNTAX) - 1));
        }
    } else {
        return(ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1));
    }

    ccode = ConnectUserToNMAPServer(client, client->user, ptr);
    if (!ccode) {
        Log(LOG_INFO, "User %s logged in from host %s", client->user, LOGIP(client->conn->socketAddress));
    } else {
        if (ccode == POP3_NMAP_SERVER_DOWN) {
            ConnWriteF(client->conn, "-ERR [SYS/PERM] %s %s\r\n", BongoGlobals.hostname, MSGERRNONMAP);
        } else if (ccode != POP3_NMAP_FEATURE_DISABLED) {
            XplDelay(2000);
            client->loginCount++;
            if (client->loginCount < MAX_LOGIN_FAILURES) {
                ConnWrite(client->conn, MSGERRNOPERMISSION, sizeof(MSGERRNOPERMISSION) - 1);
                return(0);
            }
            ConnWrite(client->conn, MSGERRNOPERMISSIONCLOSING, sizeof(MSGERRNOPERMISSIONCLOSING) - 1);
        } else {
            ConnWrite(client->conn, MSGERRNORIGHTS, sizeof(MSGERRNORIGHTS) - 1);
        }

        /* fixme - we need connection management flags for ConnClose to force the shutdown */
        ConnClose(client->conn);
        ConnFree(client->conn);
        client->conn = NULL;

        return(-1);
    }

    do
    {
        ccode = NMAPSendCommandF(client->store, "USER %s\r\n", client->user);
        if (ccode == -1) {
            ConnWriteF(client->conn, "%s %d\r\n", MSGERRNMAP, ccode);
            break;
        }
    
        ccode = NMAPReadAnswer(client->store, client->buffer, CONN_BUFSIZE, TRUE);
        switch(ccode) {
            case 3010: 
            case 3241: {
                ConnWrite(client->conn, MSGERRNOBOX, sizeof(MSGERRNOBOX) - 1);
                break;
            }
    
            case 1000: {
                ccode = 0;
                break;
            }
    
            default: {
                ConnWriteF(client->conn, "-ERR [SYS/PERM] %s %s\r\n", BongoGlobals.hostname, MSGERRNONMAP);
                break;
            }
        }
    
        if (ccode) break;
    
        ccode = NMAPSendCommandF(client->store, "STORE %s\r\n", client->user);
        if (ccode == -1) {
            ccode = ConnWriteF(client->conn, "%s %d\r\n", MSGERRNMAP, ccode);
            break;
        }
    
        ccode = NMAPReadAnswer(client->store, client->buffer, CONN_BUFSIZE, TRUE);
        switch(ccode) {
            case 3010: 
            case 3241: {
                ConnWrite(client->conn, MSGERRNOBOX, sizeof(MSGERRNOBOX) - 1);
                break;
            }
    
            case 1000: {
                ccode = 0;
                break;
            }
    
            default: {
                ConnWriteF(client->conn, "-ERR [SYS/PERM] %s %s\r\n", BongoGlobals.hostname, MSGERRNONMAP);
                break;
            }
        }
    
        if (ccode) break;
    
        if (!LoadIDList(client)) {
            ccode = -1;
            ConnWriteF(client->conn, "%s %d\r\n", MSGERRNMAP, ccode);
            break;
        }
        ccode = 0;
    } while (0);

    if (ccode == 0) {
        client->state = POP3_CLIENT_TRANSACTION;
        ConnWrite(client->conn, MSGOK, sizeof(MSGOK) - 1);

        return(ccode);
    }
    else {
        /* fixme - we need connection management flags for ConnClose to force the shutdown */
        ConnClose(client->conn);
        ConnFree(client->conn);
        client->conn = NULL;
        return(ccode);
    }
}

static int 
POP3CommandQuit(void *param)
{
    unsigned long id;
    int ccode = 0;
    int lastccode = 0;
    POP3Client *client = (POP3Client *)param;

    if (client->store && client->state == POP3_CLIENT_TRANSACTION) {
        for(id = 0; id < client->messageCount; id++) {
            if(client->message[id].deleted) {

                if (ccode != -1) {
                    ccode = NMAPSendCommandF(client->store, "DELETE %llx\r\n", client->message[id].guid);
                }
            
                if (ccode != -1) {
                    ccode = NMAPReadAnswer(client->store, client->buffer, CONN_BUFSIZE, TRUE);
                    if (ccode == 1000) {
                        ccode = 0;
                    }
                    else {
                        lastccode = ccode;
                    }
                }
            }
        }
    }

    if(lastccode == 0)
        ConnWriteF(client->conn, "+OK %s %s\r\n", BongoGlobals.hostname,MSGOKBYE);
    else
        ConnWriteF(client->conn, "-ERR %s %d, %s\r\n", MSGERRINTERNAL, lastccode, MSGOKBYE);

    ConnFlush(client->conn);
    ConnClose(client->conn);
    ConnFree(client->conn);
    client->conn = NULL;

    return(0);
}

static int
POP3CommandRetr(void *param)
{
    int ccode;
    long count = 0;
    long length;
    long tmp;
    unsigned long id;
    unsigned char *ptr;
    POP3Client *client = (POP3Client *)param;

    if (client->state == POP3_CLIENT_TRANSACTION) {
        if (client->command[4] == ' ') {
            id = atoi(client->command + 5);
        } else {
            return(ConnWrite(client->conn, MSGERRSYNTAX, sizeof(MSGERRSYNTAX) - 1));
        }
    } else {
        return(ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1));
    }

    if ((id <= 0) || (id > client->messageCount) || client->message[--id].deleted) {
        return(ConnWrite(client->conn, MSGERRNOMSG, sizeof(MSGERRNOMSG) - 1));
    }

    ccode = NMAPSendCommandF(client->store, "READ %llx\r\n", client->message[id].guid);

    if (ccode != -1) {
        ccode = NMAPReadAnswer(client->store, client->buffer, CONN_BUFSIZE, TRUE);
        switch(ccode) {
            case 2001: {
                ptr = strrchr(client->buffer, ' ');   /* find last 'space' */
                if (ptr) {
                    *ptr = '\0';
                } else {
                    return(ConnWrite(client->conn, MSGERRINTERNAL, sizeof(MSGERRINTERNAL) - 1));
                }

                ccode = ConnWrite(client->conn, MSGOK, sizeof(MSGOK) - 1);
                if (ccode != -1) {
                    /* send the message */
                    count = atoi(ptr + 1);
                    while ((count > 0) && (ccode != -1)) {
                        length = ConnReadLine(client->store, client->buffer, CONN_BUFSIZE);
                        if (length != -1) {
                            if (length > count) {
                                tmp = length;
                                length = count;
                                count -= tmp;
                            }
                            else {
                                count -= length;
                            }

                            if (client->buffer[0] != '.') {
                                ccode = ConnWrite(client->conn, client->buffer, length);
                            } else {
                                ccode = ConnWrite(client->conn, ".", 1);
                                if (ccode != -1) {
                                    ccode = ConnWrite(client->conn, client->buffer, length);
                                }
                            }
                        } else {
                            ccode = -1;
                        }
                    }

                    if (count == 0) {
                        if (NMAPReadCrLf(client->store) != 2) {
                            ccode = -1;
                        }
                    }

                    if (ccode != -1) {
                        ccode = ConnWrite(client->conn, "\r\n.\r\n", 5);
                    }

                    if (ccode != -1) {
                        /* Mark as read */
                        ccode = NMAPSendCommandF(client->store, "FLAG %llx +%lu\r\n", client->message[id].guid, (long unsigned int)STORE_MSG_FLAG_SEEN);
                    }

                    if (ccode != -1) {
                        ccode = NMAPReadAnswer(client->store, client->buffer, CONN_BUFSIZE, TRUE);
                    }

                    break;
                }
            }

            case 4220: {
                ccode = ConnWrite(client->conn, MSGERRNOMSG, sizeof(MSGERRNOMSG) - 1);
                break;
            }

            case 4222: {
                ccode = ConnWrite(client->conn, MSGERRMSGDELETED, sizeof(MSGERRMSGDELETED) - 1);
                break;
            }

            default: {
                ccode = ConnWriteF(client->conn, "%s %d\r\n",MSGERRNMAP, ccode);
                break;
            }
        }
    }

    return(ccode);
}

static int 
POP3CommandRSet(void *param)
{
    int ccode;
    unsigned long id;
    POP3Client *client = (POP3Client *)param;

    if (client->state == POP3_CLIENT_TRANSACTION) {
        for(id = 0; id < client->messageCount; id++) {
            if(client->message[id].deleted) {
                client->message[id].deleted = FALSE;
            }
        }
        client->messagesDeleted = 0;
        client->storeSizeDeleted = 0;
        ccode = ConnWriteF(client->conn, "+OK %lu %lu\r\n", client->messageCount, client->storeSize);
    } else {
        ccode = ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1);
    }


    return(ccode);
}

static int 
POP3CommandStat(void *param)
{
    int ccode;
    POP3Client *client = (POP3Client *)param;

    if (client->state == POP3_CLIENT_TRANSACTION) {
        ccode = ConnWriteF(client->conn, "+OK %lu %lu\r\n", client->messageCount - client->messagesDeleted, client->storeSize - client->storeSizeDeleted);
    } else {
        ccode = ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1);
    }

    return(ccode);
}

static int 
POP3CommandSTLS(void *param)
{
    int ccode;
    BOOL result;
    POP3Client *client = (POP3Client *)param;

    if (client->state == POP3_CLIENT_FRESH) {
        if (POP3.server.ssl.enable) {
            ccode = ConnWrite(client->conn, MSGOKSTARTTLS, sizeof(MSGOKSTARTTLS) - 1);
        } else {
            return(ConnWrite(client->conn, MSGERRUNKNOWN, sizeof(MSGERRUNKNOWN) - 1));
        }
    } else {
        return(ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1));
    }

    if (ccode != -1) {
        ccode = ConnFlush(client->conn);
    }

    if (ccode != -1) {
        client->conn->ssl.enable = TRUE;

        /* fixme - what do we want to do if encryption negotiation fails? */
        result = ConnNegotiate(client->conn, POP3.server.ssl.context);
        if (!result) {
            /* fixme - we need connection management flags for ConnClose to force the shutdown */
            ConnClose(client->conn);
            ConnFree(client->conn);
            client->conn = NULL;
        }
    }

    return(ccode);
}

static int 
POP3CommandTop(void *param)
{
    int ccode;
    size_t count;
    long length;
    long lines;
    unsigned long id;
    unsigned long headerSize;
    unsigned char *ptr;
    POP3Client *client = (POP3Client *)param;

    if (client->state == POP3_CLIENT_TRANSACTION) {
        if ((client->command[3] == ' ') && ((ptr = strchr(client->command + 4,' ')) != NULL)) {
            *ptr++ = '\0';

            id = atoi(client->command + 4);
            lines = atoi(ptr);
        } else {
            return(ConnWrite(client->conn, MSGERRSYNTAX, sizeof(MSGERRSYNTAX) - 1));
        }
    } else {
        return(ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1));
    }

    if ((id <= 0) || (id > client->messageCount) || client->message[--id].deleted) {
        return(ConnWrite(client->conn, MSGERRNOMSG, sizeof(MSGERRNOMSG) - 1));
    }

    /* Read message header size */
    ccode = NMAPGetDecimalProperty(client->store, client->message[id].guid, "nmap.mail.headersize", &headerSize);
    if (ccode == -1)
        return (ConnWriteF(client->conn, "%s %d\r\n", MSGERRNMAP, ccode));

    /* Read the message header */
    ccode = NMAPSendCommandF(client->store, "READ %llx 0 %lu\r\n", client->message[id].guid, headerSize);
    if (ccode != -1) {
        ccode = NMAPReadPropertyValueLength(client->store, "nmap.document", &count);
        switch(ccode) {
            case 2001: {
                ccode = ConnWrite(client->conn, MSGOK, sizeof(MSGOK) - 1);
                if (ccode != -1) {
                    ccode = ConnReadToConn(client->store, client->conn, count);
                }
                break;
            }

            case 4220: {
                ccode = ConnWrite(client->conn, MSGERRNOMSG, sizeof(MSGERRNOMSG) - 1);
                ccode = -1;
                break;
            }

            default: {
                ccode = ConnWriteF(client->conn, "%s %d\r\n",MSGERRNMAP, ccode);
                ccode = -1;
                break;
            }
        }
    }

    if (ccode == -1) {
        return(ccode);
    }

    if (lines) {
        ccode = NMAPSendCommandF(client->store, "READ %llx %lu %lu\r\n", client->message[id].guid, headerSize, (client->message[id].size - headerSize));
        if (ccode != -1) {
            ccode = NMAPReadPropertyValueLength(client->store, "nmap.document", &count);
            switch(ccode) {
                case 2001: {
                    /* now n lines of the body */
                    while ((count > 0) && (ccode != -1)) {
                        length = ConnReadLine(client->store, client->buffer, CONN_BUFSIZE);
                        if (length != -1) {
                            count -= length;

                            if (lines) {
                                if (client->buffer[0] != '.') {
                                    ccode = ConnWrite(client->conn, client->buffer, length);
                                } else {
                                    ccode = ConnWrite(client->conn, ".", 1);
                                    if (ccode != -1) {
                                        ccode = ConnWrite(client->conn, client->buffer, length);
                                    }
                                }

                                lines--;
                            }
                        } else {
                            ccode = -1;
                        }
                    }

                    if (ccode != -1) {
                        ccode = ConnWrite(client->conn, ".\r\n", 3);
                    }

                    break;
                }

                case 4220: {
                    ccode = ConnWrite(client->conn, MSGERRNOMSG, sizeof(MSGERRNOMSG) - 1);
                    break;
                }

                default: {
                    ccode = ConnWriteF(client->conn, "%s %d\r\n",MSGERRNMAP, ccode);
                    break;
                }
            }

            if (ccode != 2001) {
                ccode = -1;
            }
        }
    } else {
        ConnWrite(client->conn, ".\r\n", 3);
    }

    return(1000);
}

static int 
POP3CommandUIDL(void *param)
{
    int ccode;
    unsigned long id;
    POP3Client *client = (POP3Client *)param;

    if (client->state != POP3_CLIENT_TRANSACTION) {
        return(ConnWrite(client->conn, MSGERRBADSTATE, sizeof(MSGERRBADSTATE) - 1));
    }

    if (client->command[4] == ' ') {
        /* for a specific message */
        id = atoi(client->command + 5);;

        if ((id <= 0) || (id > client->messageCount) || client->message[--id].deleted) {
            return(ConnWrite(client->conn, MSGERRNOMSG, sizeof(MSGERRNOMSG) - 1));
        }

        ccode = ConnWriteF(client->conn, "+OK %ld %llx\r\n", id + 1, client->message[id].guid);

        return(ccode);
    }

    ccode = ConnWrite(client->conn, MSGOK, sizeof(MSGOK) - 1);

    if (ccode != -1) {
        for(id = 0; id < client->messageCount; id++) {
            if(client->message[id].deleted) {
                continue;
            }
            ccode = ConnWriteF(client->conn, "%lu %llx\r\n", id + 1, client->message[id].guid);
        }
        ccode = ConnWrite(client->conn, ".\r\n", 3);
    }

    return(ccode);
}


static int 
POP3CommandUser(void *param)
{
    int ccode;
    unsigned char *ptr;
    unsigned char *dest;
    unsigned char *limit;
    POP3Client *client = (POP3Client *)param;

    if (client->state == POP3_CLIENT_FRESH) {
        ptr = client->command + 5;
        while (isspace(*ptr)) {
            ptr++;
        }

        dest = client->user;
        limit = dest + sizeof(client->user);
        while (*ptr && (dest < limit)) {
            if (*ptr != ' ') {
                *dest++ = *ptr++;
                continue;
            }

            *dest++ = '_';

            ptr++;
        }

        *dest = '\0';

#if defined(NETWARE) || defined(LIBC)
        sprintf(client->buffer, "POP3: %s", client->user);

        XplRenameThread(XplGetThreadID(), client->buffer);
#endif

        client->state = POP3_CLIENT_AUTHORIZATION;

        ccode = ConnWrite(client->conn, MSGOKPASSWD, sizeof(MSGOKPASSWD) - 1);
    } else {
        ccode = ConnWrite(client->conn, MSGERRDUPNAME, sizeof(MSGERRDUPNAME) - 1);
    }

    return(ccode);
}

static int 
POP3CommandXTND(void *param)
{
    POP3Client *client = (POP3Client *)param;

    return(ConnWrite(client->conn, MSGERRNOTSUPPORTED, sizeof(MSGERRNOTSUPPORTED) - 1));
}

static BOOL
HandleConnection(void *param)
{
    int ccode;
    long threadNumber = (long)param;
    BOOL result;
    time_t last = time(NULL);
    time_t current;
    POP3Client *client;

    client = GetPOP3ClientPoolEntry();
    if (!client) {
        XplSafeDecrement(POP3.client.worker.active);

        return(FALSE);
    }

    do {
        XplRenameThread(XplGetThreadID(), "POP3 Worker");

        XplSafeIncrement(POP3.client.worker.idle);
        XplWaitOnLocalSemaphore(POP3.client.worker.todo);

        XplSafeDecrement(POP3.client.worker.idle);

        current = time(NULL);

        XplWaitOnLocalSemaphore(POP3.client.semaphore);

        client->conn = POP3.client.worker.tail;
        if (client->conn) {
            POP3.client.worker.tail = client->conn->queue.previous;
            if (POP3.client.worker.tail) {
                POP3.client.worker.tail->queue.next = NULL;
            } else {
               POP3.client.worker.head = NULL;
            }
        }

        XplSignalLocalSemaphore(POP3.client.semaphore);

        if (client->conn) {
            result = ConnNegotiate(client->conn, POP3.server.ssl.context);
            if (result) {
                if (client->conn->ssl.enable == FALSE) {
                    Log(LOG_ERROR, "Couldn't negotiate SSL with host %s", 
                        LOGIP(client->conn->socketAddress));
                } else {
                    Log(LOG_INFO, "Negotiated SSL with host %s", 
                        LOGIP(client->conn->socketAddress));
                }

                ccode = ConnWriteF(client->conn, "+OK %s %s %s\r\n", BongoGlobals.hostname, PRODUCT_NAME, PRODUCT_VERSION);

                if (ccode != -1) {
                    ccode = ConnFlush(client->conn);
                }
            } else {
                ccode = -1;
            }

            if (ccode == -1) {
                /* fixme - we need connection management flags for ConnClose to force the shutdown */
                ConnClose(client->conn);
                ConnFree(client->conn);
                client->conn = NULL;
            }
        }

        while (client->conn && (POP3.state == POP3_RUNNING)) {
            ccode = ConnReadAnswer(client->conn, client->command, CONN_BUFSIZE);
            if (ccode != -1) {
                if (ccode < CONN_BUFSIZE) {
#if 0
                    XplConsolePrintf("[%d.%d.%d.%d] Got command: %s %s\r\n", 
                        client->conn->socketAddress.sin_addr.s_net, 
                        client->conn->socketAddress.sin_addr.s_host, 
                        client->conn->socketAddress.sin_addr.s_lh, 
                        client->conn->socketAddress.sin_addr.s_impno, 
                        client->command, 
                        (client->conn->ssl.enable)? "(Secure)": "");
#endif
                    client->handler = ProtocolCommandTreeSearch(&POP3.commands, client->command);
                    if (client->handler) {
                        ccode = client->handler->handler(client);
                    } else {
                        ccode = ConnWrite(client->conn, MSGERRUNKNOWN, sizeof(MSGERRUNKNOWN) - 1);
                        Log(LOG_INFO, "Unhandled command from host %s", 
                            LOGIP(client->conn->socketAddress));
                    }

                    if ((ccode != -1) && client->conn) {
                        ConnFlush(client->conn);

                        continue;
                    }

                    break;
                }

                ccode = ConnWrite(client->conn, MSGERRUNKNOWN, sizeof(MSGERRUNKNOWN) - 1);
                ConnFlush(client->conn);
            }

            break;
        }

        if (client->store) {
            NMAPQuit(client->store);
            ConnFree(client->store);
            client->store = NULL;
        }

        if (client->conn) {
            ConnClose(client->conn);
            ConnFree(client->conn);
        }

        if (client->message != NULL) {
            MemFree(client->message);
            client->message = NULL;
        }

        /* Live or die? */
        if (threadNumber == XplSafeRead(POP3.client.worker.active)) {
            if ((current - last) > POP3.client.sleepTime) {
                break;
            }
        }

        last = time(NULL);

        memset(client, 0, sizeof(POP3Client));

        client->state = POP3_CLIENT_FRESH;
    } while (POP3.state == POP3_RUNNING);

    POP3ReturnClientPoolEntry(client);

    XplSafeDecrement(POP3.client.worker.active);

    return(TRUE);
}

static int 
ServerSocketInit (void)
{
    POP3.server.conn = ConnAlloc(FALSE);
    if (POP3.server.conn) {
        POP3.server.conn->socketAddress.sin_family = AF_INET;
        POP3.server.conn->socketAddress.sin_port = htons(POP3ServerPort);
        POP3.server.conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();
   

        /* Get root privs back for the bind.  It's ok if this fails - 
         * the user might not need to be root to bind to the port */
        XplSetEffectiveUserId(0);
   
        POP3.server.conn->socket = ConnServerSocket(POP3.server.conn, 2048);

        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            Log(LOG_ERROR, "Couldn't drop to unprivileged user %s", MsgGetUnprivilegedUser());
            return -1;
        }

   
        if (POP3.server.conn->socket == -1) {
            int ret = POP3.server.conn->socket;
            Log(LOG_ERROR, "Unable to bind to port %ld", POP3ServerPort);
            ConnFree(POP3.server.conn);
            return ret;
        }
    } else {
        Log(LOG_ERROR, "Unable to allocate create server socket");
        return -1;
    }

    return 0;
}

static int 
ServerSocketSSLInit (void)
{    
    POP3.server.ssl.conn = ConnAlloc(FALSE);
    if (POP3.server.ssl.conn) {
        POP3.server.ssl.conn->socketAddress.sin_family = AF_INET;
        POP3.server.ssl.conn->socketAddress.sin_port = htons(POP3ServerPortSSL);
        POP3.server.ssl.conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();
       
        /* Get root privs back for the bind.  It's ok if this fails - 
         * the user might not need to be root to bind to the port */
        XplSetEffectiveUserId(0);
   
        POP3.server.ssl.conn->socket = ConnServerSocket(POP3.server.ssl.conn, 2048);

        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            Log(LOG_ERROR, "Could not drop to unprivileged user %s", MsgGetUnprivilegedUser());
            return -1;
        }
       
        if (POP3.server.ssl.conn->socket == -1) {
            int ret = POP3.server.ssl.conn->socket;
            Log(LOG_ERROR, "Could not bind to secure port %ld", POP3ServerPortSSL);
            ConnFree(POP3.server.conn);
            ConnFree(POP3.server.ssl.conn);
      
            return ret;
        }
    } else {
        Log(LOG_ERROR, "Could not allocate secure socket");
        ConnFree(POP3.server.conn);
        return -1;
    }

    return 0;
}

static void 
POP3Server(void *ignored)
{
    int ccode;
    XplThreadID id;
    Connection *conn;

    XplSafeIncrement(POP3.server.active);

    XplRenameThread(XplGetThreadID(), "POP3 Server");

    POP3.state = POP3_RUNNING;

    while (POP3.state < POP3_STOPPING) {
        if (ConnAccept(POP3.server.conn, &conn) != -1) {
            if ((POP3.state < POP3_STOPPING) && !POP3.stopped) {
                conn->ssl.enable = FALSE;

                QUEUE_WORK_TO_DO(conn, id, ccode);

                if (!ccode) {
                    XplSignalLocalSemaphore(POP3.client.worker.todo);

                    continue;
                }
            } else if (POP3.stopped) {
                ccode = POP3_RECEIVER_DISABLED;
            } else {
                ccode = POP3_RECEIVER_SHUTTING_DOWN;
            }

            switch(ccode) {
                case POP3_RECEIVER_SHUTTING_DOWN: {
                    ConnSend(conn, MSGSHUTTINGDOWN, sizeof(MSGSHUTTINGDOWN) - 1);
                    break;
                }

                case POP3_RECEIVER_DISABLED: {
                    ConnSend(conn, MSGRECEIVERDOWN, sizeof(MSGRECEIVERDOWN) - 1);
                    break;
                }

                case POP3_RECEIVER_CONNECTION_LIMIT: {
                    ConnSend(conn, MSGNOCONNECTIONS, sizeof(MSGNOCONNECTIONS) - 1);
                    break;
                }

                case POP3_RECEIVER_OUT_OF_MEMORY: {
                    ConnSend(conn, MSGERRNOMEMORY, sizeof(MSGERRNOMEMORY) - 1);
                    break;
                }

                default: {
                    break;
                }
            }

            /* fixme - we need connection management flags for ConnClose to force the shutdown */
            ConnClose(conn);

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
                if (POP3.state < POP3_STOPPING) {
                    Log(LOG_ERROR, "EINTR signal received, but we're not stopping");
                }

                continue;
            }

            default: {
                if (POP3.state < POP3_STOPPING) {
                    Log(LOG_ERROR, "Exiting after an accept() failure (%d)", errno);
                }

                POP3.state = POP3_STOPPING;

                break;
            }
        }

        break;
    }

    Log(LOG_DEBUG, "Shutdown started");

    POP3.state = POP3_STOPPING;

    id = XplSetThreadGroupID(POP3.id.group);

    if (POP3.server.conn) {
        /* fixme - we need connection management flags for ConnClose to force the shutdown */
        ConnClose(POP3.server.conn);
        POP3.server.conn = NULL;
    }

    Log(LOG_DEBUG, "Standard listener down.");
    /* fixme - we need connection management flags for ConnClose to force the shutdown */
    ConnCloseAll();

    XplWaitOnLocalSemaphore(POP3.client.semaphore);

    ccode = XplSafeRead(POP3.client.worker.idle);
    while (ccode--) {
        XplSignalLocalSemaphore(POP3.client.worker.todo);
    }

    XplSignalLocalSemaphore(POP3.client.semaphore);

    for (ccode = 0; (XplSafeRead(POP3.server.active) > 1) && (ccode < 60); ccode++) {
        XplDelay(1000);
    }

    if (XplSafeRead(POP3.server.active) > 1) {
        Log(LOG_INFO, "%d server threads outstanding; attempting forceful unload.", XplSafeRead(POP3.server.active)-1);
    }

    Log(LOG_DEBUG, "Shutting down %d client threads.", XplSafeRead(POP3.client.worker.active));

    /* Make sure the kids have flown the coop. */
    for (ccode = 0; XplSafeRead(POP3.client.worker.active) && (ccode < 60); ccode++) {
        XplDelay(1000);
    }

    if (XplSafeRead(POP3.client.worker.active)) {
        Log(LOG_INFO, "%d threads outstanding; attempting forceful unload.", XplSafeRead(POP3.client.worker.active));
    }

    if (POP3.server.ssl.enable) {
        POP3.server.ssl.enable = FALSE;

        if (POP3.server.ssl.conn) {
            /* fixme - we need connection management flags for ConnClose to force the shutdown */
            ConnClose(POP3.server.ssl.conn);
            POP3.server.ssl.conn = NULL;
        }

        if (POP3.server.ssl.context) {
            ConnSSLContextFree(POP3.server.ssl.context);
            POP3.server.ssl.context = NULL;
        }
    }

    if (POP3.nmap.ssl.enable) {
        POP3.nmap.ssl.enable = FALSE;

        if (POP3.nmap.ssl.conn) {
            /* fixme - we need connection management flags for ConnClose to force the shutdown */
            ConnClose(POP3.nmap.ssl.conn);
            POP3.nmap.ssl.conn = NULL;
        }

        if (POP3.nmap.ssl.context) {
            ConnSSLContextFree(POP3.nmap.ssl.context);
            POP3.nmap.ssl.context = NULL;
        }
    }

    LogClose();

    MsgShutdown();

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();

    MemPrivatePoolFree(POP3.client.pool);

    Log(LOG_DEBUG, "Shutdown complete.");

    XplSignalLocalSemaphore(POP3.sem.main);
    XplWaitOnLocalSemaphore(POP3.sem.shutdown);

    XplCloseLocalSemaphore(POP3.sem.shutdown);
    XplCloseLocalSemaphore(POP3.sem.main);

    XplSetThreadGroupID(id);

    return;
}

static void 
POP3SSLServer(void *ignored)
{
    int ccode;
    XplThreadID id;
    Connection *conn;

    XplSafeIncrement(POP3.server.active);

    XplSignalBlock();

    XplRenameThread(XplGetThreadID(), "POP3 SSL Server");

    while (POP3.state < POP3_STOPPING) {
        if (ConnAccept(POP3.server.ssl.conn, &conn) != -1) {
            if ((POP3.state < POP3_STOPPING) && !POP3.stopped) {
                conn->ssl.enable = TRUE;

              QUEUE_WORK_TO_DO(conn, id, ccode);

                if (!ccode) {
                    XplSignalLocalSemaphore(POP3.client.worker.todo);

                    continue;
                }
            } else if (POP3.stopped) {
                ccode = POP3_RECEIVER_DISABLED;
            } else {
                ccode = POP3_RECEIVER_SHUTTING_DOWN;
            }

/* fixme - send appropriate responses */
#if 0
if (SSL_accept(client->CSSL) == 1) {
XPLIPWriteSSL(client->CSSL, "-ERR [SYS/TEMP] server error\r\n", 19);
}
SSL_free(client->CSSL);
client->CSSL = NULL;
#endif

            switch(ccode) {
                case POP3_RECEIVER_SHUTTING_DOWN: {
                    ConnSend(conn, MSGSHUTTINGDOWN, sizeof(MSGSHUTTINGDOWN) - 1);
                    break;
                }

                case POP3_RECEIVER_DISABLED: {
                    ConnSend(conn, MSGRECEIVERDOWN, sizeof(MSGRECEIVERDOWN) - 1);
                    break;
                }

                case POP3_RECEIVER_CONNECTION_LIMIT: {
                    ConnSend(conn, MSGNOCONNECTIONS, sizeof(MSGNOCONNECTIONS) - 1);
                    break;
                }

                case POP3_RECEIVER_OUT_OF_MEMORY: {
                    ConnSend(conn, MSGERRNOMEMORY, sizeof(MSGERRNOMEMORY) - 1);
                    break;
                }

                default: {
                    break;
                }
            }

            /* fixme - we need connection management flags for ConnClose to force the shutdown */
            ConnClose(conn);

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
                if (POP3.state < POP3_STOPPING) {
                    Log(LOG_ERROR, "EINTR signal received by secure server");
                }

                continue;
            }

            default: {
                if (POP3.state < POP3_STOPPING) {
                    Log(LOG_ERROR, "Secure server exiting after an accept() failure (%d)", errno);

                    POP3.state = POP3_STOPPING;
                }

                break;
            }
        }

        break;
    }

    id = XplSetThreadGroupID(POP3.id.group);

    if (POP3.server.conn) {
        /* fixme - we need connection management flags for ConnClose to force the shutdown */
        ConnClose(POP3.server.conn);
        POP3.server.conn = NULL;
    }

    POP3.server.ssl.enable = FALSE;

    if (POP3.server.ssl.conn) {
        ConnClose(POP3.server.ssl.conn);
        POP3.server.ssl.conn = NULL;
    }

    if (POP3.server.ssl.context) {
        ConnSSLContextFree(POP3.server.ssl.context);
        POP3.server.ssl.context = NULL;
    }

    XplSetThreadGroupID(id);

    XplSafeDecrement(POP3.server.active);

    Log(LOG_DEBUG, "SSL listener down.");

    return;
}

static BongoConfigItem POP3Config[] = {
        { BONGO_JSON_INT, "o:port/i", &POP3ServerPort },
        { BONGO_JSON_INT, "o:port_ssl/i", &POP3ServerPortSSL },
        { BONGO_JSON_NULL, NULL, NULL }
};

static BOOL
ReadConfiguration(void)
{
    POP3.server.ssl.config.options |= SSL_ALLOW_CHAIN;
    POP3.server.ssl.config.options |= SSL_ALLOW_SSL3;
    POP3.server.ssl.enable = TRUE;
    POP3.nmap.ssl.enable = FALSE; // FIXME: why?

    if (! MsgGetServerCredential(POP3.nmap.hash))
        return FALSE;

    if (! ReadBongoConfiguration(POP3Config, "pop3")) {
        return FALSE;
    }

    if (! ReadBongoConfiguration(GlobalConfig, "global")) {
        return FALSE;
    }

    return(TRUE);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int ccode;
    XplThreadID id;

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        Log(LOG_ERROR, "Could not drop to unprivileged user '%s'.", MsgGetUnprivilegedUser());
        return 1;
    }
    XplInit();

    POP3.id.main = XplGetThreadID();
    POP3.id.group = XplGetThreadGroupID();

    POP3.state = POP3_INITIALIZING;
    POP3.stopped = FALSE;

    XplSignalHandler(SignalHandler);

    POP3.nmap.ssl.enable = FALSE;

    POP3.client.sleepTime = 60;
    POP3.client.pool = NULL;

    POP3.server.conn = NULL;
    POP3.server.ssl.conn = NULL;
    POP3.server.ssl.enable = FALSE;
    POP3.server.ssl.context = NULL;
    POP3.server.ssl.config.options = 0;

    POP3.loggingHandle = NULL;

    POP3.managementURL[0] = '\0';
    strcpy(POP3.nmap.address, "127.0.0.1");

    XplSafeWrite(POP3.server.active, 0);

    XplSafeWrite(POP3.client.worker.idle, 0);
    XplSafeWrite(POP3.client.worker.active, 0);
    XplSafeWrite(POP3.client.worker.maximum, 100000);

    XplSafeWrite(POP3.stats.clients.serviced, 0);
    XplSafeWrite(POP3.stats.badPasswords, 0);

    LoadProtocolCommandTree(&POP3.commands, POP3CommandEntries);

    POP3.client.pool = MemPrivatePoolAlloc("Pop Connections", sizeof(POP3Client), 0, 3072, TRUE, FALSE, POP3ClientAllocCB, NULL, NULL);
    if (POP3.client.pool != NULL) {
        XplOpenLocalSemaphore(POP3.sem.main, 0);
        XplOpenLocalSemaphore(POP3.sem.shutdown, 1);
        XplOpenLocalSemaphore(POP3.client.semaphore, 1);
        XplOpenLocalSemaphore(POP3.client.worker.todo, 1);
    } else {
        Log(LOG_ERROR, "Unable to create connection pool; shutting down.");
        return(-1);
    }

    ConnStartup(CONNECTION_TIMEOUT);

    MsgInit();
    MsgAuthInit();
    NMAPInitialize();

    LogOpen("bongopop3");

    ReadConfiguration();
    CONN_TRACE_INIT(XPL_DEFAULT_WORK_DIR, "pop");
    // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */


    if (ServerSocketInit () < 0) {
        Log(LOG_ERROR, "Server Initialization failed.");
        return -1;
    }

    if (POP3.server.ssl.enable) {

        if (!ServerSocketSSLInit()) {
            POP3.server.ssl.config.certificate.file = MsgGetFile(MSGAPI_FILE_PUBKEY, NULL, 0);
            POP3.server.ssl.config.key.file = MsgGetFile(MSGAPI_FILE_PRIVKEY, NULL, 0);
            POP3.server.ssl.config.key.type = GNUTLS_X509_FMT_PEM;
    
            POP3.server.ssl.context = ConnSSLContextAlloc(&(POP3.server.ssl.config));
            if (POP3.server.ssl.context) {
                XplBeginThread(&id, POP3SSLServer, POP_STACK_SPACE, NULL, ccode);
            } else {
                POP3.server.ssl.enable = FALSE;
            }
        } else {
            POP3.server.ssl.enable = FALSE;
        }
    }

    /* Done binding, drop privs permanently */
    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        Log(LOG_ERROR, "Could not drop to unprivileged user '%s'.", MsgGetUnprivilegedUser());
        return -1;
    }

    POP3.nmap.ssl.enable = FALSE;
    POP3.nmap.ssl.config.certificate.file = MsgGetFile(MSGAPI_FILE_PUBKEY, NULL, 0);
    POP3.nmap.ssl.config.key.file = MsgGetFile(MSGAPI_FILE_PRIVKEY, NULL, 0);
    POP3.nmap.ssl.config.key.type = GNUTLS_X509_FMT_PEM;
    
    POP3.nmap.ssl.context = ConnSSLContextAlloc(&(POP3.nmap.ssl.config));
    if (POP3.nmap.ssl.context) {
        POP3.nmap.ssl.enable = TRUE;
    }

    NMAPSetEncryption(POP3.nmap.ssl.context);

    POP3.state = POP3_LOADING;

    XplStartMainThread(PRODUCT_SHORT_NAME, &id, POP3Server, 8192, NULL, ccode);

    XplUnloadApp(XplGetThreadID());
    return(0);
}
