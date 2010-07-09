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
// Parts (C) 2007 Alex Hudson. See Bongo Project Licensing.

/** \file
 *  Helpers for Bongo agents. Mainly for queue agents, also contains configuration reading helpers
 */

#include <config.h>

#include "bongoagent.h"
#include "msgapi.h"
#include "nmlib.h"

#include <execinfo.h>

#define CONNECTION_TIMEOUT (15 * 60)

#define MONITOR_SLEEP_INTERVAL 5000

struct _BongoGlobals BongoGlobals;

void 
BongoAgentHandleSignal(BongoAgent *agent,
                      int sigtype)
{
	switch(sigtype) {
	// handle various fatal errors to spurt a backtrace when possible
	case SIGILL:
	case SIGABRT:
	case SIGPIPE:
	case SIGKILL:
	case SIGSEGV: 
		{
			char path[XPL_MAX_PATH + 1];
			int boomfile;
			const int buff_size = 50;
			
			snprintf(path, XPL_MAX_PATH, "%s/guru-meditation-%d", XPL_DEFAULT_WORK_DIR, (int)time(NULL));
			boomfile = open(path, O_CREAT | O_WRONLY);
			if (boomfile != -1) {
				void * buffer[buff_size];
				int buff_used;
				
				buff_used = backtrace(buffer, buff_size);
				backtrace_symbols_fd(buffer, buff_used, boomfile);
				close(boomfile);
			}
			XplExit(-1);
		}
		break;
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

BOOL ReadConfiguration(BongoAgent *agent);

BOOL ReadConfiguration(BongoAgent *agent) {
    UNUSED_PARAMETER(agent);

	return TRUE;
}

/** 
 * Free any memory allocated as part of reading the agent's configuration. 
 * \param 	config	BongoConfigItem array which defines configuration variables
 */
void
FreeBongoConfiguration(BongoConfigItem *config) {
	while (config->type != BONGO_JSON_NULL) {
		switch (config->type) {
			case BONGO_JSON_STRING:
				if (config->destination) MemFree(config->destination);
				break;
			case BONGO_JSON_ARRAY: {
					BongoConfigItem *sub = config->destination;
					/* sub->destination here is the address of a pointer to an array */
					GArray **output = (GArray **)sub->destination;                    
					if (sub->type == BONGO_JSON_STRING) {
						unsigned int x;
						for(x=0;x<(*output)->len;x++) {
							MemFree(g_array_index(*output, char *, x));
						}
					}
                    g_array_free(*output, TRUE);
				}
				break;
			default:
				// nothing to do?
				break;
		}
		config++;
	}
}

/**
 * \internal
 * Take an individual BongoConfigItem, and a JSON tree, and try to find the config item
 * \param	schema	pointer to a single BongoConfigItem
 * \param	node	JSON tree we want to look in
 * \return		Whether or not it was successful
 */

BOOL
SetBongoConfigItem(BongoConfigItem *schema, BongoJsonNode *node) {
	if (!node || node->type != schema->type) {
		XplConsolePrintf("config: didn't find data at %s (type: %d, want: %d) \r\n", schema->source, node->type, schema->type);
		return FALSE;
	}

	switch (node->type) {
		case BONGO_JSON_BOOL: {
			BOOL *dest = (BOOL *)schema->destination;
			*dest = BongoJsonNodeAsBool(node);
			}
			break;
		case BONGO_JSON_DOUBLE: {
			double *dest = (double *)schema->destination;
			*dest = BongoJsonNodeAsDouble(node);
			}
			break;
		case BONGO_JSON_INT: {
			int *dest = (int *)schema->destination;
			*dest = BongoJsonNodeAsInt(node);
			}
			break;
		case BONGO_JSON_STRING: {
			char **dest = (char **)schema->destination;
			*dest = MemStrdup(BongoJsonNodeAsString(node));
			}
			break;
		case BONGO_JSON_ARRAY: {
			BongoConfigItem *subschema = (BongoConfigItem *)schema->destination;
			GArray **output = (GArray **)subschema->destination;
			GArray *data, *result = NULL;
			int size;

			data = BongoJsonNodeAsArray(node);
			size = data->len;
			
			switch (subschema->type) {
				case BONGO_JSON_BOOL: { 
					BOOL dest;
					int i;
                    result = g_array_sized_new(FALSE, FALSE, sizeof(BOOL), size);
					for(i = 0; i < size; i++) {
						if (BongoJsonArrayGetBool(data, i, &dest) == BONGO_JSON_OK) {
                            g_array_append_val(result, dest);
						} else {
							return FALSE;
						}
					}
					}
					break;
				case BONGO_JSON_INT: {
					int dest, i;
                    result = g_array_sized_new(FALSE, FALSE, sizeof(int), size);
					for(i = 0; i < size; i++) {
						if (BongoJsonArrayGetInt(data, i, &dest) == BONGO_JSON_OK) {
                            g_array_append_val(result, dest);
						} else {
							return FALSE;
						}
					}
					}
					break;
				case BONGO_JSON_STRING: {
					const char *dest;
					int i;
					char *newstr;
                    result = g_array_sized_new(FALSE, FALSE, sizeof(char *), size);
					for (i=0; i<size; i++) {
						if (BongoJsonArrayGetString(data, i, &dest) == BONGO_JSON_OK) {
							newstr = MemStrdup(dest);
                            g_array_append_val(result, newstr);
						} else {
							return FALSE;
						}
					}
					}
					break;
				default:
					XplConsolePrintf("config: type not yet implemented\r\n");
					break;
			}
			*output = result;
			}
			break;
		case BONGO_JSON_OBJECT:
			/* What to do here? We could take ->destination as a pointer
			   to a BongoConfigItem[], and recurse. Makes less sense for
			   objects, though */
			XplConsolePrintf("config: object type not implemented\r\n");
			break;
		default:
			XplConsolePrintf("config: unknown type %d!\r\n", node->type);
			return FALSE;
			break;
	}

	return TRUE;
}

/**
 * Read a configuration document from the Bongo Store. This is a conveniance wrapper
 * around ParseBongoConfiguration()
 * \param	config	BongoConfigItem array which defines configuration variables
 * \param	filename	Filename we wish to read from the store
 * \return	 		Whether or not we were successful
 */

BOOL 
ReadBongoConfiguration(BongoConfigItem *config, char *filename) {
	char *pconfig = NULL;
	BongoJsonNode *node = NULL;
	BOOL retcode;
	
	if (! NMAPReadConfigFile(filename, &pconfig)) {
		XplConsolePrintf("config: couldn't read config '%s' from store\r\n", filename);
		return FALSE;
	}
	if (BongoJsonParseString(pconfig, &node) != BONGO_JSON_OK) {
		retcode = FALSE;
	} else {
		retcode = ProcessBongoConfiguration(config, node);
	}
	
	if (node) BongoJsonNodeFree(node);
    if (pconfig) MemFree(pconfig);
	return retcode;
}

/**
 * Process a configuration definition
 * \param	config	BongoConfigItem array which defines configuration variables
 * \param	node	JSON node tree we want to look in
 * \return	Whether or not we were successful
 */

BOOL
ProcessBongoConfiguration(BongoConfigItem *config, BongoJsonNode *node)
{
	while (config->type != BONGO_JSON_NULL) {
		BongoJsonNode *result = BongoJsonJPath(node, config->source);
		if (!result) {
			XplConsolePrintf("config: JSON tree for schema source %s not found\r\n", config->source);
			return FALSE;
		}
		if (!SetBongoConfigItem(config, result)) {
			// can't set item  - should return FALSE here?
			XplConsolePrintf("config: schema source %s not found\r\n", config->source);
		}
		config++;
	}
	
	return TRUE;
}

int
BongoAgentInit(BongoAgent *agent,
              const char *agentName,
              const unsigned long timeOut,
              int startupResources)
{
    agent->state = BONGO_AGENT_STATE_RUNNING;

    LogOpen(agentName);

    if (startupResources & BA_STARTUP_CONNIO) {
        ConnStartup(timeOut);
    }

    if (startupResources & BA_STARTUP_MSGLIB) {
        MsgInit();
        if (startupResources & BA_STARTUP_MSGAUTH) {
            if (MsgAuthInit()) {
                Log(LOG_ERROR, "Could not initialize auth subsystem.");
                return -1;
            }
        }
    }

    if ((startupResources & BA_STARTUP_NMAP) && !NMAPInitialize()) {
        Log(LOG_ERROR, "Could not initialize nmap library.");
        return -1;
    } else {
        agent->sslContext = NMAPSSLContextAlloc();
        NMAPSetEncryption(agent->sslContext);
    }

    agent->name = MemStrdup(agentName);

    CONN_TRACE_INIT(XPL_DEFAULT_WORK_DIR, agentName);
    Log(LOG_DEBUG, "Agent Initialized");
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
  
    client = MemPrivatePoolGetEntryDirect(data->clientPool, __FILE__, __LINE__);

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

    ConnClose(data->conn);
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
  
    client = MemPrivatePoolGetEntryDirect(data->clientPool, __FILE__, __LINE__);

    memset(client, 0, data->clientSize);

    if (client == NULL) {
        XplConsolePrintf("%s: New worker failed to startup; out of memory.\r\n", data->agent->name);
        return;
    }

    if (ConnNegotiate(data->conn, data->agent->sslContext)) {
        ccode = data->handler(client, data->conn);
    }

    ConnClose(data->conn);
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
                ConnClose(conn);
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
                    Log(LOG_INFO, "accept() failed with error: %d");
                }

                continue;
            }

            default: {
                if (agent->state < BONGO_AGENT_STATE_STOPPING) {
                    Log(LOG_ERROR, "Exiting after an accept() failure; error %d.", errno);
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

    LogClose();

    MsgShutdown();

    CONN_TRACE_SHUTDOWN();
    ConnShutdown();
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
        
        reg = QueueRegister(agent->name, queue, conn->socketAddress.sin_port);
        
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
    UNUSED_PARAMETER(agent);

    *minThreads = BONGO_QUEUE_AGENT_MIN_THREADS;
    *maxThreads = BONGO_QUEUE_AGENT_MAX_THREADS;
    *minSleep = BONGO_QUEUE_AGENT_MIN_SLEEP;
}

int 
BongoQueueAgentHandshake(Connection *conn,
                        char *buffer,
                        char *qID,
                        int *envelopeLength,
                        int *messageLength)
{
    int ccode, Result = -1;
    char *ptr;
    
    ccode = NMAPReadAnswer(conn, buffer, CONN_BUFSIZE, TRUE);
    if (ccode == 6020) {
        ptr = strchr(buffer, ' ');
        if (ptr == NULL) {
            return Result;
        }

        /* copy out the qID */
        if (qID) {
            *ptr = '\0';
            strcpy(qID, buffer);
            *ptr = ' ';
        }

        Result = 0;
        ptr++;  /* increment past the space */
        if (*ptr && envelopeLength) {
            *envelopeLength = atoi(ptr);
            ptr = strchr(ptr, ' ');
            if (ptr == NULL) {
                return Result;
            }

            /* grabbing the message length only makes sense if the envelope length was there */
            ptr++;
            if (*ptr && messageLength) {
                *messageLength = atoi(ptr);
            }
        }
    }
    return Result;
}

char *
BongoQueueAgentReadEnvelope(Connection *conn,
                           char *buffer,
                           int envelopeLength,
                           int *Lines) 
{
    int ccode;
    char *envelope;
    char *cur;
    int LineCount=0;
    
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
            LineCount++;
        } else {
            line = cur + strlen(cur);
            line[1] = '\0';
            line[2] = '\0';
            LineCount++;
        }
        cur = line + 1;
    }

    if (Lines) {
        *Lines = LineCount;
    }

    return envelope;
}


void
BongoAgentShutdownFunc (BongoJsonRpcServer *server,
                       BongoJsonRpc *rpc,
                       int requestId,
                       const char *method,
                       GArray *args, 
                       void *userData)
{
    UNUSED_PARAMETER(server);
    UNUSED_PARAMETER(method);
    UNUSED_PARAMETER(args);
    UNUSED_PARAMETER(userData);

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

