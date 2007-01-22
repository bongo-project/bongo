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

/* Helpers for queue agent implementation */

#include <config.h>

#include "bongoagent.h"
#include "msgapi.h"
#include "nmlib.h"

#define CONNECTION_TIMEOUT (15 * 60)

#define MONITOR_SLEEP_INTERVAL 5000

void 
BongoAgentHandleSignal(BongoAgent *agent,
                      int sigtype)
{
    switch(sigtype) {
    case SIGHUP:
        if (agent->state < BONGO_AGENT_STATE_UNLOADING) {
            agent->state = BONGO_AGENT_STATE_UNLOADING;
        }
        
        break;
    case SIGINT:
    case SIGTERM:
        if (agent->state == BONGO_AGENT_STATE_STOPPING) {
            XplExit(0);
        } else if (agent->state < BONGO_AGENT_STATE_STOPPING) {
            agent->state = BONGO_AGENT_STATE_STOPPING;
        }
        
        break;
    default:
        break;
    }
    
    return;
}

static BOOL 
ReadConfiguration(BongoAgent *agent)
{
    MDBValueStruct *config;

    config = MDBCreateValueStruct(agent->directoryHandle, 
                                  MsgGetServerDN(NULL));
    if (config) {
        if (MDBRead(".", MSGSRV_A_OFFICIAL_NAME, config)) {
            strcpy(agent->officialName, config->Value[0]);
            MDBFreeValues(config);
        } else {
            agent->officialName[0] = '\0';
        }

        if (MDBRead(agent->name, MSGSRV_A_PORT, config)) {
            agent->port = (unsigned short)atol(config->Value[0]);
            MDBFreeValues(config);
        }

        MDBDestroyValueStruct(config);
        return TRUE;
    }

    return FALSE;
}

int
BongoAgentInit(BongoAgent *agent,
              const char *agentName,
              const char *agentDn,
              const unsigned long timeOut)
{
    agent->state = BONGO_AGENT_STATE_RUNNING;

    if (!MemoryManagerOpen(agentDn)) {
        XplConsolePrintf("%s: Unable to initialize memory manager; shutting down.\r\n", agentName);
        return -1;
    }

    ConnStartup(timeOut, TRUE);

    MDBInit();

    agent->directoryHandle = (MDBHandle)MsgInit();
    if (agent->directoryHandle == NULL) {
        XplBell();
        XplConsolePrintf("%s: Invalid directory credentials; exiting!\r\n", agentName);
        XplBell();

        MemoryManagerClose(agentDn);

        return -1;
    }

    if (!NMAPInitialize(agent->directoryHandle)) {
        XplConsolePrintf("%s: Could not initialize nmap library\r\n", agentName);
        return -1;
    }

    SetCurrentNameSpace(NWOS2_NAME_SPACE);
    SetTargetNameSpace(NWOS2_NAME_SPACE);

    agent->loggingHandle = LoggerOpen(agentDn);
    if (!agent->loggingHandle) {
        XplConsolePrintf("%s: Unable to initialize logging; disabled.\r\n", agentName);
    }

    agent->sslContext = NMAPSSLContextAlloc();
    NMAPSetEncryption(agent->sslContext);

    agent->name = MemStrdup(agentName);
    agent->dn = MemStrdup(agentDn);

    /* Read general agent configuration */
    ReadConfiguration(agent);
    CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), agentName);
    // CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */
    
    return 0;
}

typedef struct {
    BongoAgent *agent;
    Connection *conn;
    void *clientPool;
    BongoAgentClientFree clientFree;
    BongoAgentClientHandler handler;
    int clientSize;
} ListenCallbackData;

static void
QueueHandleConnection (void *datap)
{
    ListenCallbackData *data = datap;
    void *client;
    int ccode;
    char line[CONN_BUFSIZE];
  
#if defined(RELEASE_BUILD)
    client = MemPrivatePoolGetEntry(data->clientPool);
#else
    client = MemPrivatePoolGetEntryDebug(data->clientPool, __FILE__, __LINE__);
#endif

    memset(client, 0, data->clientSize);

    if (client == NULL) {
        XplConsolePrintf("%s: New worker failed to startup; out of memory.\r\n", data->agent->name);

        NMAPSendCommand(data->conn, "QDONE\r\n", 7);
        NMAPReadAnswer(data->conn, line, CONN_BUFSIZE, FALSE);
        return;
    }

    if (ConnNegotiate(data->conn, data->agent->sslContext)) {
        ccode = data->handler(client, data->conn);
    }

    NMAPSendCommand(data->conn, "QDONE\r\n", 7);
    NMAPReadAnswer(data->conn, line, CONN_BUFSIZE, FALSE);

    ConnClose(data->conn, 1);
    ConnFree(data->conn);
    
    data->clientFree(client);
    MemFree(data);
}

static void
HandleConnection (void *datap) 
{
    ListenCallbackData *data = datap;
    void *client;
    int ccode;
  
#if defined(RELEASE_BUILD)
    client = MemPrivatePoolGetEntry(data->clientPool);
#else
    client = MemPrivatePoolGetEntryDebug(data->clientPool, __FILE__, __LINE__);
#endif

    memset(client, 0, data->clientSize);

    if (client == NULL) {
        XplConsolePrintf("%s: New worker failed to startup; out of memory.\r\n", data->agent->name);
        return;
    }

    if (ConnNegotiate(data->conn, data->agent->sslContext)) {
        ccode = data->handler(client, data->conn);
    }

    ConnClose(data->conn, 1);
    ConnFree(data->conn);
    
    data->clientFree(client);
    MemFree(data);
}

static void
ListenInternal(BongoAgent *agent,
               Connection *serverConn,
               BongoThreadPool *threadPool,
               BongoThreadPoolHandler connectionHandler,
               void *clientPool,
               BongoAgentClientFree clientFree,
               int clientSize,
               BongoAgentClientHandler clientHandler,
               BOOL queueSocket)
{
    while (agent->state < BONGO_AGENT_STATE_STOPPING) {
        Connection *conn;
        if (ConnAccept(serverConn, &conn) != -1) {
            if (agent->state < BONGO_AGENT_STATE_STOPPING) {
                void *data;
                conn->ssl.enable = FALSE;
                
                if (clientPool) {
                    ListenCallbackData *clientData;
                    clientData = MemMalloc(sizeof(ListenCallbackData));
                    clientData->conn = conn;
                    clientData->agent = agent;
                    clientData->handler = clientHandler;
                    clientData->clientFree = clientFree;
                    clientData->clientPool = clientPool;
                    clientData->clientSize = clientSize;

                    data = clientData;
                } else {
                    data = conn;
                }
                
                BongoThreadPoolAddWork(threadPool, 
                                      (BongoThreadPoolHandler)connectionHandler,
                                      data);
            } else {
                if (queueSocket) {
                    ConnWrite(conn, "QDONE\r\n", 7);
                }
                ConnClose(conn, 0);
                ConnFree(conn);
                conn = NULL;
            }
            continue;
        }

        switch (errno) {
            case ECONNABORTED:
#ifdef EPROTO
            case EPROTO: 
#endif
            case EINTR: {
                if (agent->state < BONGO_AGENT_STATE_STOPPING) {
                    LoggerEvent(agent->loggingHandle, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);
                }

                continue;
            }

            default: {
                if (agent->state < BONGO_AGENT_STATE_STOPPING) {
                    XplConsolePrintf("%s: Exiting after an accept() failure; error %d\r\n", agent->name, errno);

                    LoggerEvent(agent->loggingHandle, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_ACCEPT_FAILURE, LOG_ERROR, 0, "Server", NULL, errno, 0, NULL, 0);

                    agent->state = BONGO_AGENT_STATE_STOPPING;
                }

                break;
            }
        }

        break;
    }
}

void
BongoQueueAgentListenWithClientPool(BongoAgent *agent,
                                   Connection *serverConn,
                                   BongoThreadPool *threadPool,
                                   int clientSize,
                                   BongoAgentClientFree clientFree,
                                   BongoAgentClientHandler handler,
                                   void **memPool)
{
    void *clientPool = MemPrivatePoolAlloc("QueueClientConnections", 
                                           clientSize,
                                           0, BONGO_QUEUE_AGENT_MAX_CLIENTS, 
                                           TRUE, FALSE, 
                                           NULL, 
                                           NULL, 
                                           NULL);
    if (memPool) {
        *memPool = clientPool;
    }

    if (clientPool == NULL) {
        agent->state = BONGO_AGENT_STATE_STOPPING;
        XplConsolePrintf("%s: Unable to create connection pool.\r\n", agent->name);
        return;
    }

    ListenInternal(agent,
                   serverConn,
                   threadPool,
                   QueueHandleConnection,
                   clientPool,
                   clientFree,
                   clientSize,
                   handler, 
                   TRUE);

/*    MemPrivatePoolFree(clientPool);    ** FIXME: see bug 204732 */
}

void
BongoAgentListenWithClientPool(BongoAgent *agent,
                              Connection *serverConn,
                              BongoThreadPool *threadPool,
                              int clientSize,
                              int maxClients,
                              BongoAgentClientFree clientFree,
                              BongoAgentClientHandler handler,
                              void **memPool)
{
    void *clientPool = MemPrivatePoolAlloc("ClientConnections", 
                                           clientSize,
                                           0, maxClients, 
                                           TRUE, FALSE, 
                                           NULL, 
                                           NULL, 
                                           NULL);
    if (memPool) {
        *memPool = clientPool;
    }

    if (clientPool == NULL) {
        agent->state = BONGO_AGENT_STATE_STOPPING;
        XplConsolePrintf("%s: Unable to create connection pool.\r\n", agent->name);
        return;
    }

    ListenInternal(agent,
                   serverConn,
                   threadPool,
                   HandleConnection,
                   clientPool,
                   clientFree,
                   clientSize,
                   handler,
                   FALSE);

/*    MemPrivatePoolFree(clientPool);    ** FIXME: see bug 204732 */
}


void 
BongoAgentListen(BongoAgent *agent,
                Connection *serverConn,
                BongoThreadPool *threadPool,
                BongoAgentConnectionHandler handler)
{
    ListenInternal(agent,
                   serverConn,
                   threadPool,
                   (BongoThreadPoolHandler)handler,
                   NULL,
                   NULL,
                   0,
                   NULL, 
                   FALSE);
}

void 
BongoQueueAgentListen(BongoAgent *agent,
                     Connection *serverConn,
                     BongoThreadPool *threadPool,
                     BongoAgentConnectionHandler handler)
{
    ListenInternal(agent,
                   serverConn,
                   threadPool,
                   (BongoThreadPoolHandler)handler,
                   NULL,
                   NULL,
                   0,
                   NULL,
                   FALSE);
}

void
BongoAgentShutdown(BongoAgent *agent)
{
    if (agent->sslContext) {
        ConnSSLContextFree(agent->sslContext);
    }

    ConnCloseAll(1);

    LoggerClose(agent->loggingHandle);

    MsgShutdown();

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();

    MemoryManagerClose(agent->dn); /* Use the appropriate MSGSRV_AGENT_NAME macro. */    
}

Connection * 
BongoQueueConnectionInit(BongoAgent *agent, 
                        int queue)
{
    Connection *conn;

    conn = ConnAlloc(FALSE);
    if (conn) {
        int reg;
        memset(&(conn->socketAddress), 0, sizeof(conn->socketAddress));

        conn->socketAddress.sin_family = AF_INET;
        conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();

        conn->socket = ConnServerSocket(conn, 2048);

        if (conn->socket == -1) {
            XplConsolePrintf("%s: Could not bind to dynamic port\r\n", agent->name);
            ConnFree(conn);
            return NULL;
        }
        
        reg = NMAPRegister(agent->dn, 
                           queue,
                           conn->socketAddress.sin_port);
        
        if (reg != REGISTRATION_COMPLETED) {
            XplConsolePrintf("%s: Could not register with bongonmap (%d)\r\n", agent->name, reg);
            ConnFree(conn);
            return NULL;
        }
    } else {
        XplConsolePrintf("%s: Could not allocate connection.\r\n", agent->name);
        return NULL;
    }
    return conn;
}

void
BongoQueueAgentGetThreadPoolParameters(BongoAgent *agent,
                                      int *minThreads,
                                      int *maxThreads,
                                      int *minSleep)
{
    *minThreads = BONGO_QUEUE_AGENT_MIN_THREADS;
    *maxThreads = BONGO_QUEUE_AGENT_MAX_THREADS;
    *minSleep = BONGO_QUEUE_AGENT_MIN_SLEEP;
}

int 
BongoQueueAgentHandshake(Connection *conn,
                        char *buffer,
                        char *qID,
                        int *envelopeLength)
{
    int ccode;
    char *ptr;
    
    if (((ccode = NMAPReadAnswer(conn,
                                 buffer, 
                                 CONN_BUFSIZE, 
                                 TRUE)) != -1) 
        && (ccode == 6020) 
        && ((ptr = strchr(buffer, ' ')) != NULL)) {
        *ptr++ = '\0';
        
        strcpy(qID, buffer);
        
        *envelopeLength = atoi(ptr);

        return 0;
    } else {
        return -1;
    }
}

char *
BongoQueueAgentReadEnvelope(Connection *conn,
                           char *buffer,
                           int envelopeLength) 
{
    int ccode;
    char *envelope;
    char *cur;
    
    envelope = MemMalloc(envelopeLength + 5);
    if (XPL_UNLIKELY (envelope == NULL)) {
        return NULL;
    }
    
    ccode = NMAPReadCount(conn, envelope, envelopeLength);
    if (XPL_UNLIKELY(ccode != envelopeLength)) {
        MemFree(envelope);
        return NULL;
    }
    
    ccode = NMAPReadAnswer(conn, buffer, CONN_BUFSIZE, TRUE);
    if (XPL_UNLIKELY (ccode != 6021)) {
        MemFree(envelope);
        return NULL;
    }

    envelope[envelopeLength] = '\0';

    /* Split the envelope into lines.  A line might be terminated by
     * one or two nulls, and the whole envelope will be terminated by
     * three */
    cur = envelope;
    while (ccode != -1 && *cur) {
        char *line;
        line = strchr(cur, 0x0A);
        if (line) {
            *line = '\0';
            if (line[-1] == 0x0D) {
                *(line - 1) = '\0';
            }
        } else {
            line = cur + strlen(cur);
            line[1] = '\0';
            line[2] = '\0';
        }
        cur = line + 1;
    }

    return envelope;
}


void
BongoAgentShutdownFunc (BongoJsonRpcServer *server,
                       BongoJsonRpc *rpc,
                       int requestId,
                       const char *method,
                       BongoArray *args, 
                       void *userData)
{
    kill (getpid (), SIGTERM);
    
    /* Might not make it here */
    BongoJsonRpcSuccess (rpc, requestId, NULL);
}

/* Special cases are here so we can tweak when they're called */

void 
BongoAgentAddConfigMonitor(BongoAgent *agent,
                          BongoAgentMonitorCallback configMonitor)
{
    BongoAgentAddMonitor(agent, configMonitor, 300000);
}

void 
BongoAgentAddDiskMonitor(BongoAgent *agent, 
                        BongoAgentMonitorCallback diskMonitor)
{
    BongoAgentAddMonitor(agent, diskMonitor, 600000);
}

void 
BongoAgentAddLoadMonitor(BongoAgent *agent, 
                        BongoAgentMonitorCallback loadMonitor)
{
    BongoAgentAddMonitor(agent, loadMonitor, 5000);
}

/* Add a monitor.  This isn't threadsafe, all monitors should be added
 * before starting the monitor thread */
void
BongoAgentAddMonitor(BongoAgent *agent,
                    BongoAgentMonitorCallback callback,
                    int interval)
{
    if (agent->maxMonitors == agent->numMonitors) {
        if (agent->maxMonitors == 0) {
            agent->maxMonitors = 5;
        } else {
            agent->maxMonitors *= 2;
        }

        agent->monitors = MemRealloc(agent->monitors, sizeof(BongoAgentMonitor) * agent->maxMonitors);
    }

    agent->monitors[agent->numMonitors].callback = callback;
    agent->monitors[agent->numMonitors++].interval = interval / MONITOR_SLEEP_INTERVAL;
}

static void
Monitor(void *agentp) 
{
    BongoAgent *agent = agentp;
    int iteration = 0;
    
    while (agent->state < BONGO_AGENT_STATE_STOPPING) {
        int i;
        
        iteration++;
        
        for (i = 0; i < agent->numMonitors; i++) {
            if ((agent->monitors[i].interval % iteration) == 0) {
                agent->monitors[i].callback(agent);
            }
        }

        XplDelay(MONITOR_SLEEP_INTERVAL);
    }
}

void 
BongoAgentStartMonitor(BongoAgent *agent)
{
    int ccode;
    
    XplBeginThread(&agent->monitorID, Monitor, 10240, agent, ccode);
}

