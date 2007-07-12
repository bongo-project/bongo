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

/* Helpers for agent implementation */

#ifndef BONGOAGENT_H
#define BONGOAGENT_H

#include <xpl.h>
#include <mdb.h>
#include <logger.h>
#include <bongothreadpool.h>
#include <connio.h>
#include <bongomanagee.h>

/* FIXME: need to tweak these */
#define BONGO_QUEUE_AGENT_DEFAULT_STACK_SIZE (80 * 1024)
#define BONGO_QUEUE_AGENT_MAX_CLIENTS 1024
#define BONGO_QUEUE_AGENT_MIN_THREADS 5
#define BONGO_QUEUE_AGENT_MAX_THREADS 20
#define BONGO_QUEUE_AGENT_MIN_SLEEP (5 * 60)

/** Helpers for all agents **/  

typedef struct _BongoAgent BongoAgent;

typedef void (*BongoAgentConnectionHandler)(Connection *conn);
typedef int (*BongoAgentClientHandler)(void *client, Connection *conn);
typedef void (*BongoAgentClientFree)(void *client);

typedef void (*BongoAgentMonitorCallback)(BongoAgent *agent);

typedef enum _BongoAgentStates {
    BONGO_AGENT_STATE_RUNNING, 
    BONGO_AGENT_STATE_UNLOADING, 
    BONGO_AGENT_STATE_STOPPING, 

    BONGO_AGENT_STATE_MAX_STATES
} BongoAgentState;

#define BA_STARTUP_XPL     0
#define BA_STARTUP_MDB     1
#define BA_STARTUP_CONNIO  2
#define BA_STARTUP_NMAP    4
#define BA_STARTUP_LOGGER  8

typedef struct {
    int interval;
    BongoAgentMonitorCallback callback;
} BongoAgentMonitor;

struct _BongoAgent {
    const char *name;
    const char *dn;
    
    unsigned short port;

    BongoAgentState state;

    MDBHandle directoryHandle;
    LoggingHandle *loggingHandle;
    bongo_ssl_context *sslContext;

    unsigned char officialName[MAXEMAILNAMESIZE + 1];

    int numMonitors;
    int maxMonitors;
    BongoAgentMonitor *monitors;
    
    XplThreadID monitorID;
    
    BongoManagee *managee;
};

/* Configuration file reading stuff */

typedef struct _BongoConfigItem {
	BongoJsonType type;
	char *source;
	void *destination;
} BongoConfigItem;

BOOL ReadBongoConfiguration(BongoConfigItem *config, char *filename);
BOOL SetBongoConfigItem(BongoConfigItem *schema, BongoJsonNode *node);
void FreeBongoConfiguration(BongoConfigItem *config);

int BongoAgentInit(BongoAgent *agent,
                  const char *agentName,
                  const char *agentDn,
                  const unsigned long timeOut,
                  int startupResources);
void BongoAgentHandleSignal(BongoAgent *agent,
                           int sigtype);
void BongoAgentShutdown(BongoAgent *agent);


/**  Helpers for Queue agents **/

Connection *BongoQueueConnectionInit(BongoAgent *agent, int queue);

/* Monitor thread management */

/* Special case monitors, so we can tweak when they're called*/
void BongoAgentAddConfigMonitor(BongoAgent *agent,
                               BongoAgentMonitorCallback configMonitor);

void BongoAgentAddDiskMonitor(BongoAgent *agent,
                             BongoAgentMonitorCallback configMonitor);

void BongoAgentAddLoadMonitor(BongoAgent *agent,
                             BongoAgentMonitorCallback configMonitor);

void BongoAgentAddMonitor(BongoAgent *agent,
                         BongoAgentMonitorCallback monitor,
                         int interval);

void BongoAgentStartMonitor(BongoAgent *agent);

/* Listen on a socket, calling handler for each connection */
void BongoAgentListen(BongoAgent *agent,
                     Connection *serverConn,
                     BongoThreadPool *threadPool,
                     BongoAgentConnectionHandler handler);

/* Same for queue agents, calls QDONE when the handler is complete*/
void BongoQueueAgentListen(BongoAgent *agent,
                          Connection *serverConn,
                          BongoThreadPool *threadPool,
                          BongoAgentConnectionHandler handler);

/* Listen on a socket, call handler back with an allocated client
 * struct*/
void BongoAgentListenWithClientPool(BongoAgent *agent,
                                   Connection *serverConn,
                                   BongoThreadPool *threadPool,
                                   int clientSize,
                                   int maxClients,
                                   BongoAgentClientFree clientFree,
                                   BongoAgentClientHandler handler,
                                   void **memPool);

/* Same for queue agents, calls QDONE when the handler is complete*/
void BongoQueueAgentListenWithClientPool(BongoAgent *agent,
                                        Connection *serverConn,
                                        BongoThreadPool *threadPool,
                                        int clientSize,
                                        BongoAgentClientFree clientFree,
                                        BongoAgentClientHandler handler,
                                        void **memPool);

void BongoQueueAgentGetThreadPoolParameters(BongoAgent *agent,
                                           int *minThreads,
                                           int *maxThreads,
                                           int *minSleep);

int BongoQueueAgentHandshake(Connection *conn,
                            char *buffer,
                            char *qID,
                            int *envelopeLength);

char *BongoQueueAgentReadEnvelope(Connection *conn,
                                 char *buffer,
                                 int envelopeLength);

/* BongoManagee Functions */
void BongoAgentShutdownFunc (BongoJsonRpcServer *server, 
                            BongoJsonRpc *rpc, 
                            int requestId,
                            const char *method,
                            BongoArray *args, 
                            void *userData);

#define BONGO_ENVELOPE_NEXT(p) \
    { (p) = (p) + strlen(p) + 1;                 \
        if (*(p) == '\0') (p)++;                 \
    }

#endif
