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
#include <memmgr.h>
#include <logger.h>
#include <bongoutil.h>
#include <bongoagent.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>

#include "generic.h"

GAgentGlobals GAgent = {{0,}, 0, };

static void 
GAgentClientFree(void *clientp)
{
    GAgentClient *client = clientp;

    if (client->envelope) {
        MemFree(client->envelope);
    }

    MemPrivatePoolReturnEntry(client);
}

static int 
ProcessEntry(void *clientp,
             Connection *conn)
{
    int ccode;
    int envelopeLength;
    int messageLength;
    char *ptr;
    char qID[16];
    GAgentClient *client = clientp;

    client->conn = conn;

    ccode = BongoQueueAgentHandshake(client->conn, 
                                    client->line, 
                                    qID, 
                                    &envelopeLength,
                                    &messageLength);

    sprintf(client->line, "GAgent: %s", qID);
    XplRenameThread(XplGetThreadID(), client->line);

    if (ccode == -1) {
        return -1;
    }

    client->envelope = BongoQueueAgentReadEnvelope(client->conn, 
                                                  client->line, 
                                                  envelopeLength,
                                                  NULL);

    if (client->envelope == NULL) {
        return -1;
    }
    
    /* Queue-agent specific code.  This code just prints out the envelope */
    ptr = client->envelope;
    while (*ptr) {
        switch(*ptr++) {
        case QUEUE_FROM :
            XplConsolePrintf("bongogeneric: message from: %s\n", ptr);
            break;
        case QUEUE_CALENDAR_LOCAL:
            XplConsolePrintf("bongogeneric: deliver to calendar: %s\n", ptr);
            break;
        case QUEUE_RECIP_LOCAL :
        case QUEUE_RECIP_MBOX_LOCAL :
            XplConsolePrintf("bongogeneric: deliver to local: %s\n", ptr);
            break;
        case QUEUE_FLAGS:
            XplConsolePrintf("bongogeneric: flags: %s\n", ptr);
            break;
        case QUEUE_ADDRESS:
            XplConsolePrintf("bongogeneric: address: %s\n", ptr);
            break;
        case QUEUE_BOUNCE:
            XplConsolePrintf("bongogeneric: bounce: %s\n", ptr);
            break;
        case QUEUE_DATE:
            XplConsolePrintf("bongogeneric: date: %s\n", ptr);
            break;
        case QUEUE_ID:
            XplConsolePrintf("bongogeneric: queue id: %s\n", ptr);
            break;
        case QUEUE_RECIP_REMOTE:
            XplConsolePrintf("bongogeneric: deliver to remote: %s\n", ptr);
            break;
        case QUEUE_THIRD_PARTY:
            XplConsolePrintf("bongogeneric: third party: %s\n", ptr);
            break;
        }
        BONGO_ENVELOPE_NEXT(ptr);
    }

    /* The client struct, connection, and QDONE will be cleaned up by
     * the caller */
    return 0;
}

static void 
GenericAgentServer(void *ignored)
{
    XplRenameThread(XplGetThreadID(), AGENT_DN " Server");

    /* Listen for incoming queue items.  Call ProcessEntry with a
     * GAgentClient allocated for each incoming queue entry. */
    BongoQueueAgentListenWithClientPool(&GAgent.agent,
                                       GAgent.nmapConn,
                                       GAgent.threadPool,
                                       sizeof(GAgentClient),
                                       GAgentClientFree, 
                                       ProcessEntry,
                                       &GAgent.clientPool);

    /* Shutting down */
    XplConsolePrintf(AGENT_NAME ": Shutting down.\r\n");

    if (GAgent.nmapConn) {
        ConnClose(GAgent.nmapConn, 1);
        GAgent.nmapConn = NULL;
    }

    BongoThreadPoolShutdown(GAgent.threadPool);
    BongoAgentShutdown(&GAgent.agent);

    XplConsolePrintf(AGENT_NAME ": Shutdown complete\r\n");
}

static BOOL 
ReadConfiguration(void)
{
    return TRUE;
}

static void 
SignalHandler(int sigtype) 
{
    BongoAgentHandleSignal(&GAgent.agent, sigtype);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int ccode;
    int minThreads;
    int maxThreads;
    int minSleep;
    int startupOpts;

    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf(AGENT_NAME ": Could not drop to unprivileged user '%s'\r\n" AGENT_NAME ": exiting.\n", MsgGetUnprivilegedUser());
        return -1;
    }
    XplInit();

    strcpy(GAgent.nmapAddress, "127.0.0.1");

    /* Initialize the Bongo libraries */
    startupOpts = BA_STARTUP_CONNIO | BA_STARTUP_NMAP;
    ccode = BongoAgentInit(&GAgent.agent, AGENT_NAME, DEFAULT_CONNECTION_TIMEOUT, startupOpts);
    if (ccode == -1) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    /* Set up socket for listening on an incoming queue */ 
    GAgent.queueNumber = Q_FIVE;
    GAgent.nmapConn = BongoQueueConnectionInit(&GAgent.agent, GAgent.queueNumber);

    if (!GAgent.nmapConn) {
        BongoAgentShutdown(&GAgent.agent);
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    BongoQueueAgentGetThreadPoolParameters(&GAgent.agent,
                                          &minThreads, &maxThreads, &minSleep);
    
    /* Create a thread pool for managing connections */
    GAgent.threadPool = BongoThreadPoolNew(AGENT_DN " Clients",
                                          BONGO_QUEUE_AGENT_DEFAULT_STACK_SIZE,
                                          minThreads, maxThreads, minSleep);

    if (GAgent.threadPool == NULL) {
        BongoAgentShutdown(&GAgent.agent);
        XplConsolePrintf(AGENT_NAME ": Unable to create thread pool.\r\n" AGENT_NAME ": Exiting.\r\n");
        return -1;
    }
    
    ReadConfiguration();

    XplSignalHandler(SignalHandler);

    /* Start the server thread */
    XplStartMainThread(AGENT_NAME, &id, GenericAgentServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    
    return 0;
}
