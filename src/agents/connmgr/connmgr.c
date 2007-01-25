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

/* Product defines */
#define PRODUCT_SHORT_NAME "connmgr.nlm"
#define PRODUCT_NAME "Bongo Connection Manager Agent"
#define PRODUCT_DESCRIPTION "Allows agents to keep and retrieve information about hosts that have talked to the system."
#define PRODUCT_VERSION "$Revision: 1.5 $"

#include <xpl.h>
#include <xplresolve.h>
#include <bongoutil.h>
#include <connio.h>
#include <mdb.h>
#include <msgapi.h>
#include <logger.h>
#include <nmap.h>
#include <management.h>

#include "connmgrp.h"

#if !defined(DEBUG)
#define GetConnMgrClientPoolEntry() MemPrivatePoolGetEntry(ConnMgr.client.pool)
#else
#define GetConnMgrClientPoolEntry() MemPrivatePoolGetEntryDebug(ConnMgr.client.pool, __FILE__, __LINE__)
#endif

static void SignalHandler(int sigtype);

#define QUEUE_WORK_TO_DO(c, id, r) \
{ \
    XplWaitOnLocalSemaphore(ConnMgr.client.semaphore); \
    if (XplSafeRead(ConnMgr.client.worker.idle)) { \
        (c)->queue.previous = NULL; \
        if (((c)->queue.next = ConnMgr.client.worker.head) != NULL) { \
            (c)->queue.next->queue.previous = (c); \
        } else { \
            ConnMgr.client.worker.tail = (c); \
        } \
        ConnMgr.client.worker.head = (c); \
        (r) = 0; \
    } else if (XplSafeRead(ConnMgr.client.worker.active) < XplSafeRead(ConnMgr.client.worker.maximum)) { \
        XplSafeIncrement(ConnMgr.client.worker.active); \
        XplSignalBlock(); \
        XplBeginThread(&(id), HandleConnection, CONNMGR_STACK_SPACE, XPL_INT_TO_PTR(XplSafeRead(ConnMgr.client.worker.active)), (r)); \
        XplSignalHandler(SignalHandler); \
        if (!(r)) { \
            (c)->queue.previous = NULL; \
            if (((c)->queue.next = ConnMgr.client.worker.head) != NULL) { \
                (c)->queue.next->queue.previous = (c); \
            } else { \
                ConnMgr.client.worker.tail = (c); \
            } \
            ConnMgr.client.worker.head = (c); \
        } else { \
            XplSafeDecrement(ConnMgr.client.worker.active); \
            (r) = CONNMGR_RECEIVER_OUT_OF_MEMORY; \
        } \
    } else { \
        (r) = CONNMGR_RECEIVER_CONNECTION_LIMIT; \
    } \
    XplSignalLocalSemaphore(ConnMgr.client.semaphore); \
}

enum ConnMgrStates {
    CONNMGR_STARTING = 0, 
    CONNMGR_INITIALIZING, 
    CONNMGR_LOADING, 
    CONNMGR_RUNNING, 
    CONNMGR_RELOADING, 
    CONNMGR_UNLOADING, 
    CONNMGR_STOPPING, 
    CONNMGR_DONE, 

    CONNMGR_MAX_STATES
};

enum ConnMgrReceiverStates {
    CONNMGR_RECEIVER_SHUTTING_DOWN = 1, 
    CONNMGR_RECEIVER_DISABLED, 
    CONNMGR_RECEIVER_CONNECTION_LIMIT, 
    CONNMGR_RECEIVER_OUT_OF_MEMORY, 

    CONNMGR_RECEIVER_MAX_STATES
};

enum ConnMgrClientStates {
    CONNMGR_CLIENT_FRESH = 0, 
    CONNMGR_CLIENT_AUTHORIZED, 
    CONNMGR_CLIENT_ENDING, 

    CONNMGR_CLIENT_MAX_STATES
};

typedef struct _ConnMgrClient {
    enum ConnMgrClientStates state;
    Connection *conn;

    ConnMgrCommand *command;
    ConnMgrResult reply;
} ConnMgrClient;

struct {
    enum ConnMgrStates state;
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
        Connection *conn;
        XplAtomic active;
        struct sockaddr_in addr;
        unsigned char hash[CM_HASH_SIZE];
        unsigned char host[MAXEMAILNAMESIZE + 1];
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
            XplAtomic serviced;
        } clients;

        XplAtomic badPasswords;
    } stats;

    struct {
        XplRWLock lock;

        unsigned long *addresses;
        unsigned char **handles;
        unsigned char **salts;
        unsigned long used;
        unsigned long allocated;
    } authHandles;

    MDBHandle directoryHandle;
    void *loggingHandle;

    ModuleStruct *modules;
    unsigned char moduleDir[XPL_MAX_PATH + 1];
} ConnMgr;

static BOOL HandleConnection(void *param);

static BOOL ReadConnMgrVariable(unsigned int Variable, unsigned char *Data, size_t *DataLength);
static BOOL WriteConnMgrVariable(unsigned int Variable, unsigned char *Data, size_t DataLength);

static BOOL ConnMgrShutdown(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
static BOOL ConnMgrDMCCommandHelp(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);
static BOOL SendConnMgrStatistics(unsigned char *Arguments, unsigned char **Response, BOOL *CloseConnection);

ManagementVariables    ConnMgrManagementVariables[] = {
    /* 0  PRODUCT_VERSION                        */ { DMCMV_REVISIONS,               DMCMV_REVISIONS_HELP,               ReadConnMgrVariable, NULL    }, 
    /* 1  ConnMgr.client.worker.maximum          */ { DMCMV_MAX_CONNECTION_COUNT,    DMCMV_MAX_CONNECTION_COUNT_HELP,    ReadConnMgrVariable, WriteConnMgrVariable    }, 
    /* 2  ConnMgr.server.active                  */ { DMCMV_SERVER_THREAD_COUNT,     DMCMV_SERVER_THREAD_COUNT_HELP,     ReadConnMgrVariable, NULL    }, 
    /* 3  ConnMgr.client.worker.active           */ { DMCMV_CONNECTION_COUNT,        DMCMV_CONNECTION_COUNT_HELP,        ReadConnMgrVariable, NULL    }, 
    /* 4  ConnMgr.client.worker.idle             */ { DMCMV_IDLE_CONNECTION_COUNT,   DMCMV_IDLE_CONNECTION_COUNT_HELP,   ReadConnMgrVariable, NULL    }, 
    /* 5  ConnMgr.stopped                        */ { DMCMV_RECEIVER_DISABLED,       DMCMV_RECEIVER_DISABLED_HELP,       ReadConnMgrVariable, WriteConnMgrVariable    }, 
    /* 6  ConnMgr.stats.clients.serviced         */ { DMCMV_TOTAL_CONNECTIONS,       DMCMV_TOTAL_CONNECTIONS_HELP,       ReadConnMgrVariable, WriteConnMgrVariable    },
    /* 7  ConnMgr.stats.badPasswords             */ { DMCMV_BAD_PASSWORD_COUNT,      DMCMV_BAD_PASSWORD_COUNT_HELP,      ReadConnMgrVariable, WriteConnMgrVariable    },
    /* 8                                         */ { DMCMV_VERSION,                 DMCMV_VERSION_HELP,                 ReadConnMgrVariable, NULL    }, 
};

ManagementCommands ConnMgrManagementCommands[] = {
    /* 0  HELP[ <Command>]  */ { DMCMC_HELP,                    ConnMgrDMCCommandHelp    }, 
    /* 1  SHUTDOWN          */ { DMCMC_SHUTDOWN,                ConnMgrShutdown          },
    /* 2  STATS             */ { DMCMC_STATS,                   SendConnMgrStatistics    }, 
    /* 3  MEMORY            */ { DMCMC_DUMP_MEMORY_USAGE,       ManagementMemoryStats       },
    /* 4  CONNTRACEFLAGS    */ { DMCMC_CONN_TRACE_USAGE,   ManagementConnTrace  },
};


static BOOL 
ConnMgrClientAllocCB(void *buffer, void *clientData)
{
    ConnMgrClient *c = (ConnMgrClient *)buffer;

    memset(c, 0, sizeof(ConnMgrClient));

    c->state = CONNMGR_CLIENT_FRESH;
    return(TRUE);
}

static void 
ConnMgrReturnClientPoolEntry(ConnMgrClient *client)
{
    register ConnMgrClient *c = client;

    memset(c, 0, sizeof(ConnMgrClient));

    c->state = CONNMGR_CLIENT_FRESH;

    MemPrivatePoolReturnEntry(c);

    return;
}

static BOOL
HandleConnection(void *param)
{
    long threadNumber = (long)param;
    time_t last = time(NULL);
    time_t current;
    ConnMgrClient *client;

    client = GetConnMgrClientPoolEntry();
    if (!client) {
        XplSafeDecrement(ConnMgr.client.worker.active);

        return(FALSE);
    }

    do {
        XplRenameThread(XplGetThreadID(), "Connection Manager Worker");

        XplSafeIncrement(ConnMgr.client.worker.idle);
		XplWaitOnLocalSemaphore(ConnMgr.client.worker.todo);
        XplSafeDecrement(ConnMgr.client.worker.idle);

        current = time(NULL);

		XplWaitOnLocalSemaphore(ConnMgr.client.semaphore);

        client->conn = ConnMgr.client.worker.tail;
		if (client->conn) {
            ConnMgr.client.worker.tail = client->conn->queue.previous;
			if (ConnMgr.client.worker.tail) {
				ConnMgr.client.worker.tail->queue.next = NULL;
			} else {
				ConnMgr.client.worker.head = NULL;
			}
		}

		XplSignalLocalSemaphore(ConnMgr.client.semaphore);

        if (client->conn) {
            /*
                Pull the request out of client->conn.receive.buffer.  (See large comment in ConnMgrServer)
                client->conn.receive.buffer has been abused to deliver the request to the client.
            */
            client->command = (ConnMgrCommand *)client->conn->receive.buffer;
            LoggerEvent(ConnMgr.loggingHandle, LOGGER_SUBSYSTEM_AUTH, LOGGER_EVENT_CONNECTION, LOG_INFO, 0, NULL, NULL, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
        }

#if 0
        XplConsolePrintf("[%d.%d.%d.%d] Got command: %lu\r\n", 
                client->conn->socketAddress.sin_addr.s_net, 
                client->conn->socketAddress.sin_addr.s_host, 
                client->conn->socketAddress.sin_addr.s_lh, 
                client->conn->socketAddress.sin_addr.s_impno, 
                client->command.request);
#endif

        if (client->command) {
            unsigned long i;
            BOOL allowed = FALSE;
            BOOL found = FALSE;
            unsigned char salt[256 + 1];

            salt[0] = '\0';
            client->command->pass[32] = '\0';
            /*
                Check the pass string provided in command.  If it is not correct for the given
                address, then generate a salt and send it.  The client can then repeat the
                command with a valid pass.
            */
            XplRWReadLockAcquire(&ConnMgr.authHandles.lock);
            for (i = 0; i < ConnMgr.authHandles.used; i++) {

                if (ConnMgr.authHandles.addresses[i] == client->conn->socketAddress.sin_addr.s_addr) {
                    found = TRUE;
                    if (XplStrCaseCmp(client->command->pass, ConnMgr.authHandles.handles[i]) == 0) {
                        allowed = TRUE;
                    } else {
                        strcpy(salt, ConnMgr.authHandles.salts[i]);
                    }
                }
            }

            if (!found) {
                xpl_hash_context ctx;
                unsigned char handle[XPLHASH_MD5_LENGTH];

                XplRWReadLockRelease(&ConnMgr.authHandles.lock);

                snprintf(salt, sizeof(salt), "%x%s%x", XplGetThreadID(), ConnMgr.server.host, (unsigned int)time(NULL)); 
                salt[sizeof(salt) - 1] = 0;

                XplHashNew(&ctx, XPLHASH_MD5);
                XplHashWrite(&ctx, salt, strlen(salt));
                XplHashWrite(&ctx, ConnMgr.server.hash, CM_HASH_SIZE);
                XplHashFinal(&ctx, XPLHASH_LOWERCASE, handle, XPLHASH_MD5_LENGTH);

                XplRWWriteLockAcquire(&ConnMgr.authHandles.lock);
                /* Generate the salt and the pass */
                if (ConnMgr.authHandles.used + 1 > ConnMgr.authHandles.allocated) {
                    ConnMgr.authHandles.allocated += 5;

                    ConnMgr.authHandles.addresses = MemRealloc(ConnMgr.authHandles.addresses, sizeof(unsigned long) * ConnMgr.authHandles.allocated);
                    ConnMgr.authHandles.handles = MemRealloc(ConnMgr.authHandles.handles, sizeof(unsigned char *) * ConnMgr.authHandles.allocated);
                    ConnMgr.authHandles.salts = MemRealloc(ConnMgr.authHandles.salts, sizeof(unsigned char *) * ConnMgr.authHandles.allocated);
                }

                ConnMgr.authHandles.addresses[ConnMgr.authHandles.used] = client->conn->socketAddress.sin_addr.s_addr;
                ConnMgr.authHandles.handles[ConnMgr.authHandles.used] = MemStrdup(handle);
                ConnMgr.authHandles.salts[ConnMgr.authHandles.used] = MemStrdup(salt);
                ConnMgr.authHandles.used++;

                XplRWWriteLockRelease(&ConnMgr.authHandles.lock);
            }

            XplRWReadLockRelease(&ConnMgr.authHandles.lock);

            if (allowed) {
                /*
                    If command->address == 0, then resolve the host in command->host and place the
                    resulting value in command->addres.  To keep the command size down host and user
                    are in a union.  This means you can not provide a host when doing a user related
                    command.
                */
                client->command->address = ntohl(client->command->address);

                switch (client->command->command) {
                    case CM_COMMAND_VERIFY: {
                        ModulesVerify(ConnMgr.modules, client->command, &client->reply);
                        break;
                    }

                    case CM_COMMAND_NOTIFY: {
                        ModulesNotify(ConnMgr.modules, client->command);
                        client->reply.result = CM_RESULT_OK;
                        break;
                    }

                    default: {
                        LoggerEvent(ConnMgr.loggingHandle, LOGGER_SUBSYSTEM_UNHANDLED, LOGGER_EVENT_UNHANDLED_REQUEST, LOG_INFO, 0, NULL, NULL, XplHostToLittle(client->conn->socketAddress.sin_addr.s_addr), 0, NULL, 0);
                        client->reply.result = CM_RESULT_UNKNOWN_COMMAND;
                        break;
                    }
                }

                sendto(ConnMgr.server.conn->socket, (unsigned char *)&client->reply, sizeof(ConnMgrResult), 0, (struct sockaddr *)&client->conn->socketAddress, sizeof(client->conn->socketAddress));
            } else {
                /* send a result with the salt */
                client->reply.result = CM_RESULT_MUST_AUTH;
                strcpy(client->reply.detail.salt, salt);
                sendto(ConnMgr.server.conn->socket, (unsigned char *)&client->reply, sizeof(ConnMgrResult), 0, (struct sockaddr *)&client->conn->socketAddress, sizeof(client->conn->socketAddress));
            }
        }

        if (client->conn) {
            ConnClose(client->conn, 0);
            ConnFree(client->conn);
        }

		/* Live or die? */
        if (threadNumber == XplSafeRead(ConnMgr.client.worker.active)) {
            if ((current - last) > ConnMgr.client.sleepTime) {
                break;
            }
        }

        last = time(NULL);

        memset(client, 0, sizeof(ConnMgrClient));

        client->state = CONNMGR_CLIENT_FRESH;
    } while (ConnMgr.state == CONNMGR_RUNNING);

    ConnMgrReturnClientPoolEntry(client);

    XplSafeDecrement(ConnMgr.client.worker.active);

    return(TRUE);
}

static int 
ServerSocketInit(void)
{
    ConnMgr.server.conn = ConnAlloc(FALSE);
    if (ConnMgr.server.conn) {
        ConnMgr.server.conn->socketAddress.sin_family = AF_INET;
        ConnMgr.server.conn->socketAddress.sin_port = htons(CM_PORT);
        ConnMgr.server.conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();

        /* Get root privs back for the bind.  It's ok if this fails - 
         * the user might not need to be root to bind to the port */
        XplSetEffectiveUserId(0);

        /* Get our socket, and bind.  This is normally done with ConnServerSocket() but that is TCP only */
        ConnMgr.server.conn->socket = IPsocket(PF_INET, SOCK_DGRAM, IPPROTO_UDP);
        if (ConnMgr.server.conn->socket != -1) {
            int ccode;

            setsockopt(ConnMgr.server.conn->socket, SOL_SOCKET, SO_REUSEADDR, (unsigned char *)&ccode, sizeof(ccode));
            ccode = IPbind(ConnMgr.server.conn->socket, (struct sockaddr *)&ConnMgr.server.conn->socketAddress, sizeof(ConnMgr.server.conn->socketAddress));
            if (ccode == -1) {
                IPclose(ConnMgr.server.conn->socket);
                ConnMgr.server.conn->socket = -1;
            }
        }

        if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
            XplConsolePrintf("bongoconnmgr: Could not drop to unprivileged user '%s'\n", MsgGetUnprivilegedUser());
            return(-1);
        }
        
        if (ConnMgr.server.conn->socket == -1) {
            int ret = ConnMgr.server.conn->socket;
            XplConsolePrintf("bongoconnmgr: Could not bind to port %d\n", CM_PORT);
            ConnFree(ConnMgr.server.conn);
            return(ret);
        }
    } else {
        XplConsolePrintf("bongoconnmgr: Could not allocate connection.\n");
        return(-1);
    }

    return(0);
}

static void
ConnMgrServer(void *ignored)
{
    XplThreadID id;
    int ccode;
    Connection *conn;
    unsigned long i;

    XplSafeIncrement(ConnMgr.server.active);
    XplRenameThread(XplGetThreadID(), "Connection Manager Server");
    ConnMgr.state = CONNMGR_RUNNING;

    while (ConnMgr.state < CONNMGR_STOPPING) {
        ConnMgrCommand command;
        long rc = -1;

        /* Must call connAlloc() with FALSE because we do not need the buffers for UDP */
        conn = ConnAlloc(FALSE);
        if (conn) {
            int size = 0;
            ConnMgrResult result;

            while ((ConnMgr.state < CONNMGR_STOPPING) && ((rc == -1) || (rc == EOF) || (size == 0))) {
                size = sizeof(conn->socketAddress);
                rc = recvfrom(ConnMgr.server.conn->socket, (unsigned char *)&command, sizeof(ConnMgrCommand), 0, (struct sockaddr *)&conn->socketAddress, &size);
            }


            if ((ConnMgr.state < CONNMGR_STOPPING) && !ConnMgr.stopped) {
                conn->ssl.enable = FALSE;

                /*
                    Abuse conn.receive.buffer.  This is a pointer in the Connection structure
                    that is not being used in this case, because we are calling ConnAlloc()
                    with FALSE.  Since in UDP we don't have accept, we are just calling recvfrom
                    and the result will contain the entire request.  We MUST get this request
                    to the client thread.  The only easy way to do that without changing ConnIO
                    is to put it into the Connection structure somewhere.
                */
                conn->receive.buffer = (unsigned char *) MemMalloc(sizeof(ConnMgrCommand));
                memcpy(conn->receive.buffer, &command, sizeof(ConnMgrCommand));

                QUEUE_WORK_TO_DO(conn, id, ccode);
                if (!ccode) {
                    XplSignalLocalSemaphore(ConnMgr.client.worker.todo);

                    continue;
                }
            } else if (ConnMgr.stopped) {
                ccode = CONNMGR_RECEIVER_DISABLED;
            } else {
                ccode = CONNMGR_RECEIVER_SHUTTING_DOWN;
            }

            memset(&result, 0, sizeof(ConnMgrResult));
            switch(ccode) {
                case CONNMGR_RECEIVER_SHUTTING_DOWN: {
                    result.result = CM_RESULT_SHUTTING_DOWN;
                    break;
                }

                case CONNMGR_RECEIVER_DISABLED: {
                    result.result = CM_RESULT_RECEIVER_DISABLED;
                    break;
                }

                case CONNMGR_RECEIVER_CONNECTION_LIMIT: {
                    result.result = CM_RESULT_CONNECTION_LIMIT;
                    break;
                }

                case CONNMGR_RECEIVER_OUT_OF_MEMORY: {
                    result.result = CM_RESULT_NO_MEMORY;
                    break;
                }

                default: {
                    break;
                }
            }

            if (result.result >= CM_RESULT_FIRST_ERROR) {
                sendto(ConnMgr.server.conn->socket, (unsigned char *)&result, sizeof(ConnMgrResult), 0, (struct sockaddr *)&conn->socketAddress, sizeof(conn->socketAddress));
            }

            /* fixme - we need connection management flags for ConnClose to force the shutdown */
            ConnClose(conn, 1);

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
                if (ConnMgr.state < CONNMGR_STOPPING) {
                    LoggerEvent(ConnMgr.loggingHandle, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);
                }

                continue;
            }

            default: {
                if (ConnMgr.state < CONNMGR_STOPPING) {
#if VERBOSE
                    XplConsolePrintf("bongoconnmgr: Exiting after an recvfrom() failure; error %d\n", errno);
#endif

                    LoggerEvent(ConnMgr.loggingHandle, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);
                }

                ConnMgr.state = CONNMGR_STOPPING;

                break;
            }
        }

        break;
    }

    /* Shutting down */
#if VERBOSE
    XplConsolePrintf("bongoconnmgr: Shutting down.\r\n");
#endif
    ConnMgr.state = CONNMGR_STOPPING;

    id = XplSetThreadGroupID(ConnMgr.id.group);

    if (ConnMgr.server.conn) {
        /* fixme - we need connection management flags for ConnClose to force the shutdown */
        ConnClose(ConnMgr.server.conn, 1);
        ConnMgr.server.conn = NULL;
    }

    /* fixme - we need connection management flags for ConnClose to force the shutdown */
    ConnCloseAll(1);

    /* Management Client Shutdown */
    if (ManagementState() != MANAGEMENT_STOPPED) {
        ManagementShutdown();

	    for (ccode = 0; (ManagementState() != MANAGEMENT_STOPPED) && (ccode < 60); ccode++) {
		    XplDelay(1000);
	    }
    }

    XplWaitOnLocalSemaphore(ConnMgr.client.semaphore);

    ccode = XplSafeRead(ConnMgr.client.worker.idle);
    while (ccode--) {
        XplSignalLocalSemaphore(ConnMgr.client.worker.todo);
    }

    XplSignalLocalSemaphore(ConnMgr.client.semaphore);

    for (ccode = 0; (XplSafeRead(ConnMgr.server.active) > 1) && (ccode < 60); ccode++) {
        XplDelay(1000);
    }

    if (XplSafeRead(ConnMgr.server.active) > 1) {
        XplConsolePrintf("bongoconnmgr: %d server threads outstanding; attempting forceful unload.\r\n", XplSafeRead(ConnMgr.server.active) - 1);
    }

#if VERBOSE
    XplConsolePrintf("\rbongoconnmgr: Shutting down %d client threads\r\n", XplSafeRead(ConnMgr.client.worker.active));
#endif

    /* Make sure the kids have flown the coop. */
    for (ccode = 0; XplSafeRead(ConnMgr.client.worker.active) && (ccode < 60); ccode++) {
        XplDelay(1000);
    }

    if (XplSafeRead(ConnMgr.client.worker.active)) {
        XplConsolePrintf("\rbongoconnmgr: %d threads outstanding; attempting forceful unload.\r\n", XplSafeRead(ConnMgr.client.worker.active));
    }

    XPLCryptoLockDestroy();    

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();

    /* Close down the modules */
    if (ConnMgr.modules) {
        UnloadModules(ConnMgr.modules);
    }

    LoggerClose(ConnMgr.loggingHandle);
    ConnMgr.loggingHandle = NULL;

    MsgShutdown();
/*  MDBShutdown(); */

    MemPrivatePoolFree(ConnMgr.client.pool);
    MemoryManagerClose(MSGSRV_AGENT_CONNMGR);

    XplSignalLocalSemaphore(ConnMgr.sem.main);
    XplWaitOnLocalSemaphore(ConnMgr.sem.shutdown);

    XplCloseLocalSemaphore(ConnMgr.sem.shutdown);
    XplCloseLocalSemaphore(ConnMgr.sem.main);

    XplRWLockDestroy(&ConnMgr.authHandles.lock);

    for (i = 0; i < ConnMgr.authHandles.used; i++) {
        MemFree(ConnMgr.authHandles.handles[i]);
    }
    MemFree(ConnMgr.authHandles.handles);
    MemFree(ConnMgr.authHandles.addresses);

#if VERBOSE
    XplConsolePrintf("\rbongoconnmgr: Shutdown complete\r\n");
#endif
    XplSetThreadGroupID(id);

    return;
}

static BOOL
ReadConfiguration(void)
{
    unsigned long i;
    MDBValueStruct *config;
    
    config = MDBCreateValueStruct(ConnMgr.directoryHandle, NULL);

    if (config && MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, config)) {
        gethostname(ConnMgr.server.host, sizeof(ConnMgr.server.host));
        HashCredential(MsgGetServerDN(NULL), config->Value[0], ConnMgr.server.hash);

        MDBFreeValues(config);
    } else {
        return(FALSE);
    }

    MDBSetValueStructContext(MsgGetServerDN(NULL), config);
    if (MDBRead(MSGSRV_AGENT_CONNMGR, MSGSRV_A_CONFIGURATION, config)) {
        for (i = 0; i < config->Used; i++) {
            if (XplStrNCaseCmp(config->Value[i], "MaxLoad:", 8)==0) {
                XplSafeWrite(ConnMgr.client.worker.maximum, atol(config->Value[i] + 8));
            }
        }

        MDBFreeValues(config);
    }

	MsgGetLibDir(ConnMgr.moduleDir);
	strcat(ConnMgr.moduleDir, "/connmgr");
	MsgMakePath(ConnMgr.moduleDir);

    MDBDestroyValueStruct(config);

    return(TRUE);
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
int 
_NonAppCheckUnload(void)
{
    int            s;
    static BOOL    checked = FALSE;
    XplThreadID    id;

    if (!checked) {
        checked = ConnMgr.state = CONNMGR_UNLOADING;

        XplWaitOnLocalSemaphore(ConnMgr.sem.shutdown);

        id = XplSetThreadGroupID(ConnMgr.id.group);
        if (ConnMgr.server.conn->socket != -1) {
            s = ConnMgr.server.conn->socket;
            ConnMgr.server.conn->socket = -1;

            IPclose(s);
        }
        XplSetThreadGroupID(id);

        XplWaitOnLocalSemaphore(ConnMgr.sem.main);
    }

    return(0);
}
#endif

static void 
SignalHandler(int sigtype)
{
    switch(sigtype) {
        case SIGHUP: {
            if (ConnMgr.state < CONNMGR_UNLOADING) {
                ConnMgr.state = CONNMGR_UNLOADING;
            }

            break;
        }
        case SIGINT :
        case SIGTERM: {
            if (ConnMgr.state == CONNMGR_STOPPING) {
                XplUnloadApp(getpid());
            } else if (ConnMgr.state < CONNMGR_STOPPING) {
                ConnMgr.state = CONNMGR_STOPPING;
            }

            break;
        }

        default: {
            break;
        }
    }

    return;
}

static BOOL 
ConnMgrShutdown(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    XplThreadID id;

    if (response) {
        if (!arguments) {
            if (ConnMgr.server.conn && (ConnMgr.server.conn->socket != -1)) {
                *response = MemStrdup("Shutting down.\r\n");
                if (*response) {
                    id = XplSetThreadGroupID(ConnMgr.id.group);

                    ConnMgr.state = CONNMGR_STOPPING;

                    if (ConnMgr.server.conn) {
                        ConnClose(ConnMgr.server.conn, 1);
                        ConnMgr.server.conn = NULL;
                    }

                    if (closeConnection) {
                        *closeConnection = TRUE;
                    }

                    XplSetThreadGroupID(id);
                }
            } else if (ConnMgr.state >= CONNMGR_STOPPING) {
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

static BOOL 
ConnMgrDMCCommandHelp(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    BOOL responded = FALSE;

    if (response) {
        if (arguments) {
            switch(toupper(arguments[0])) {
                case 'M': {
                    if (XplStrCaseCmp(arguments, DMCMC_DUMP_MEMORY_USAGE) == 0) {
                        if ((*response = MemStrdup(DMCMC_DUMP_MEMORY_USAGE_HELP)) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    }
                }

                case 'C': {
                    if (XplStrNCaseCmp(arguments, DMCMC_CONN_TRACE_USAGE, sizeof(DMCMC_CONN_TRACE_USAGE) - 1) == 0) {
                        if ((*response = MemStrdup(DMCMC_CONN_TRACE_USAGE_HELP)) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    }
                }

                case 'S': {
                    if (XplStrCaseCmp(arguments, DMCMC_SHUTDOWN) == 0) {
                        if ((*response = MemStrdup(DMCMC_SHUTDOWN_HELP)) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    } else if (XplStrCaseCmp(arguments, DMCMC_STATS) == 0) {
                        if ((*response = MemStrdup(DMCMC_STATS_HELP)) != NULL) {
                            responded = TRUE;
                        }

                        break;
                    }
                }

                default: {
                    break;
                }
            }
        } else if ((*response = MemStrdup(DMCMC_HELP_HELP)) != NULL) {
            responded = TRUE;
        }

        if (responded || ((*response = MemStrdup(DMCMC_UNKOWN_COMMAND)) != NULL)) {
            return(TRUE);
        }
    }

    return(FALSE);
}

static BOOL 
SendConnMgrStatistics(unsigned char *arguments, unsigned char **response, BOOL *closeConnection)
{
    MemStatistics poolStats;

    if (!arguments && response) {
        memset(&poolStats, 0, sizeof(MemStatistics));

        *response = MemMalloc(sizeof(PRODUCT_NAME)      /* Long Name                        */
                        + sizeof(PRODUCT_SHORT_NAME)    /* Short Name                       */
                        + 10                            /* PRODUCT_MAJOR_VERSION            */
                        + 10                            /* PRODUCT_MINOR_VERSION            */
                        + 10                            /* PRODUCT_LETTER_VERSION           */
                        + 10                            /* Connection Pool Allocation Count */
                        + 10                            /* Connection Pool Memory Usage     */
                        + 10                            /* Connection Pool Pitches          */
                        + 10                            /* Connection Pool Strikes          */
                        + 10                            /* DMCMV_SERVER_THREAD_COUNT        */
                        + 10                            /* DMCMV_CONNECTION_COUNT           */
                        + 10                            /* DMCMV_IDLE_CONNECTION_COUNT      */
                        + 10                            /* DMCMV_MAX_CONNECTION_COUNT       */
                        + 10                            /* DMCMV_TOTAL_CONNECTIONS          */
                        + 10                            /* DMCMV_BAD_PASSWORD_COUNT         */
                        + 28);                          /* Formatting                       */

        MemPrivatePoolStatistics(ConnMgr.client.pool, &poolStats);

        if (*response) {
            sprintf(*response, "%s (%s: v%d.%d.%d)\r\n%lu:%lu:%lu:%lu:%d:%d:%d:%d:%d:%d\r\n", 
                    PRODUCT_NAME, 
                    PRODUCT_SHORT_NAME, 
                    PRODUCT_MAJOR_VERSION, 
                    PRODUCT_MINOR_VERSION, 
                    PRODUCT_LETTER_VERSION, 
                    poolStats.totalAlloc.count, 
                    poolStats.totalAlloc.size, 
                    poolStats.pitches, 
                    poolStats.strikes, 
                    XplSafeRead(ConnMgr.server.active), 
                    XplSafeRead(ConnMgr.client.worker.active), 
                    XplSafeRead(ConnMgr.client.worker.idle), 
                    XplSafeRead(ConnMgr.client.worker.maximum), 
                    XplSafeRead(ConnMgr.stats.clients.serviced), 
                    XplSafeRead(ConnMgr.stats.badPasswords));

            return(TRUE);
        }

        if ((*response = MemStrdup("Out of memory.\r\n")) != NULL) {
            return(TRUE);
        }
    } else if ((arguments) && ((*response = MemStrdup("arguments not allowed.\r\n")) != NULL)) {
        return(TRUE);
    }

    return(FALSE);
}

static BOOL 
ReadConnMgrVariable(unsigned int variable, unsigned char *data, size_t *dataLength)
{
    size_t count;
    unsigned char *ptr;

    switch (variable) {
        case 0: {
            unsigned char    version[30];

            PVCSRevisionToVersion(PRODUCT_VERSION, version);
            count = strlen(version) + 10;

            if (data && (*dataLength > count)) {
                ptr = data;

                PVCSRevisionToVersion(PRODUCT_VERSION, version);
                ptr += sprintf(ptr, "connmgr.c: %s\r\n", version);

                *dataLength = ptr - data;
            } else {
                *dataLength = count;
            }

            break;
        }

        case 1: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(ConnMgr.client.worker.maximum));
            }

            *dataLength = 12;
            break;
        }

        case 2: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(ConnMgr.server.active));
            }

            *dataLength = 12;
            break;
        }

        case 3: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(ConnMgr.client.worker.active));
            }

            *dataLength = 12;
            break;
        }

        case 4: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(ConnMgr.client.worker.idle));
            }

            *dataLength = 12;
            break;
        }

        case 5: {
            if (ConnMgr.stopped == FALSE) {
                ptr = "FALSE\r\n";
                count = 7;
            } else {
                ptr = "TRUE\r\n";
                count = 6;
            }

            if (data && (*dataLength > count)) {
                strcpy(data, ptr);
            }

            *dataLength = count;
            break;
        }

        case 6: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(ConnMgr.stats.clients.serviced));
            }

            *dataLength = 12;
            break;
        }

        case 7: {
            if (data && (*dataLength > 12)) {
                sprintf(data, "%010d\r\n", XplSafeRead(ConnMgr.stats.badPasswords));
            }

            *dataLength = 12;
            break;
        }

        case 8: {
            DMC_REPORT_PRODUCT_VERSION(data, *dataLength);
            break;
        }

        default: {
            *dataLength = 0;
            return(FALSE);
        }
    }

    return(TRUE);
}

static BOOL 
WriteConnMgrVariable(unsigned int variable, unsigned char *data, size_t dataLength)
{
    unsigned char *ptr;
    unsigned char *ptr2;
    BOOL result = TRUE;

    if (!data || !dataLength) {
        return(FALSE);
    }

    switch (variable) {
        case 1: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(ConnMgr.client.worker.maximum, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 5: {
            if ((toupper(data[0]) == 'T') || (atol(data) != 0)) {
                ConnMgr.stopped = TRUE;
            } else if ((toupper(data[0] == 'F')) || (atol(data) == 0)) {
                ConnMgr.stopped = FALSE;
            } else {
                result = FALSE;
            }

            break;
        }

        case 9: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(ConnMgr.stats.clients.serviced, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 10: {
            ptr = strchr(data, '\n');
            if (ptr) {
                *ptr = '\0';
            }

            ptr2 = strchr(data, '\r');
            if (ptr2) {
                ptr2 = '\0';
            }

            XplSafeWrite(ConnMgr.stats.badPasswords, atol(data));

            if (ptr) {
                *ptr = '\n';
            }

            if (ptr2) {
                *ptr2 = 'r';
            }

            break;
        }

        case 0:
        case 2:
        case 3:
        case 4:
        case 6:
        case 7:
        case 8:
        default: {
            result = FALSE;
            break;
        }
    }

    return(result);
}



XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int ccode;
    XplThreadID id;

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("bongoconnmgr: Could not drop to unprivileged user '%s', exiting.\n", MsgGetUnprivilegedUser());
        return(-1);
    }
    XplInit();
    ConnMgr.id.main = XplGetThreadID();
    ConnMgr.id.group = XplGetThreadGroupID();

    ConnMgr.state = CONNMGR_INITIALIZING;
    ConnMgr.stopped = FALSE;

    XplSignalHandler(SignalHandler);

    ConnMgr.client.sleepTime = 60;
    ConnMgr.client.pool = NULL;

    ConnMgr.server.conn = NULL;

    ConnMgr.directoryHandle = NULL;
    ConnMgr.loggingHandle = NULL;

    XplSafeWrite(ConnMgr.server.active, 0);

    XplSafeWrite(ConnMgr.client.worker.idle, 0);
    XplSafeWrite(ConnMgr.client.worker.active, 0);
    XplSafeWrite(ConnMgr.client.worker.maximum, 100000);

    XplSafeWrite(ConnMgr.stats.clients.serviced, 0);
    XplSafeWrite(ConnMgr.stats.badPasswords, 0);

    if (MemoryManagerOpen(MSGSRV_AGENT_CONNMGR) == TRUE) {
        ConnMgr.client.pool = MemPrivatePoolAlloc("Connection Manager Connections", sizeof(ConnMgrClient), 0, 3072, TRUE, FALSE, ConnMgrClientAllocCB, NULL, NULL);
        if (ConnMgr.client.pool != NULL) {
            XplOpenLocalSemaphore(ConnMgr.sem.main, 0);
            XplOpenLocalSemaphore(ConnMgr.sem.shutdown, 1);
            XplOpenLocalSemaphore(ConnMgr.client.semaphore, 1);
            XplOpenLocalSemaphore(ConnMgr.client.worker.todo, 0);
        } else {
            MemoryManagerClose(MSGSRV_AGENT_CONNMGR);

            XplConsolePrintf("bongoconnmgr: Unable to create connection pool; shutting down.\r\n");
            return(-1);
        }
    } else {
        XplConsolePrintf("bongoconnmgr: Unable to initialize memory manager; shutting down.\r\n");
        return(-1);
    }

    ConnStartup(CONNECTION_TIMEOUT, TRUE);

    MDBInit();
    ConnMgr.directoryHandle = (MDBHandle)MsgInit();
    if (ConnMgr.directoryHandle == NULL) {
        XplBell();
        XplConsolePrintf("\rbongoconnmgr: Invalid directory credentials; exiting!\n");
        XplBell();

        MemoryManagerClose(MSGSRV_AGENT_CONNMGR);

        return(-1);
    }

    ConnMgr.loggingHandle = LoggerOpen("bongoconnmgr");
    if (ConnMgr.loggingHandle == NULL) {
        XplConsolePrintf("bongoconnmgr: Unable to initialize logging interface.  Logging disabled.\r\n");
    }

    if (!ReadConfiguration()) {
        XplConsolePrintf("bongoconnmgr: Unable to read configuration.  Exiting.\r\n");
        return(-1);
    }
    CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), "connmgr");
    // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */


    XplRWLockInit(&ConnMgr.authHandles.lock);

    /* Load the modules */
    ConnMgr.modules = LoadModules(ConnMgr.moduleDir, NULL);
    if (!ConnMgr.modules) {
        XplConsolePrintf("bongoconnmgr: Failed to load any modules.  Functionality will be limited.\n");
    }

    if (ServerSocketInit () < 0) {
        XplConsolePrintf("bongoconnmgr: Exiting.\n");
        return(-1);
    }

    /* Done binding, drop privs permanentely */
    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("bongoconnmgr: Could not drop to unprivileged user '%s', exiting.\n", MsgGetUnprivilegedUser());
        return(-1);
    }

    /* Management Client Startup */
    if ((ManagementInit(MSGSRV_AGENT_CONNMGR, ConnMgr.directoryHandle)) 
            && (ManagementSetVariables(ConnMgrManagementVariables, sizeof(ConnMgrManagementVariables) / sizeof(ManagementVariables))) 
            && (ManagementSetCommands(ConnMgrManagementCommands, sizeof(ConnMgrManagementCommands) / sizeof(ManagementCommands)))) {
        XplBeginThread(&id, ManagementServer, DMC_MANAGEMENT_STACKSIZE, NULL, ccode);
    }

    ConnMgr.state = CONNMGR_LOADING;

    XplStartMainThread(PRODUCT_SHORT_NAME, &id, ConnMgrServer, 8192, NULL, ccode);

    XplUnloadApp(XplGetThreadID());
    return(0);
}
