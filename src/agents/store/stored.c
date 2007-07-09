/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005 Novell, Inc. All Rights Reserved.
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
#include <mdb.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>

#include "stored.h"
#include "messages.h"

#define STACKSPACE (128 * 1024)

#define MEMSTACKSIZE (4 * 1024)

struct StoreGlobals StoreAgent;

static void 
StoreClientFree(void *clientp)
{
    StoreClient *client = clientp;

    UnselectStore(client);

    UnselectUser(client);

    if (client->system.alarmdb) {
        AlarmDBClose(client->system.alarmdb);
    }

    BongoMemStackDestroy(&client->memstack);

    MemPrivatePoolReturnEntry(client);
}

static int 
ProcessEntry(void *clientp,
             Connection *conn)
{
    int ccode;
    int count;
    StoreClient *client;

    client = clientp;
    client->conn = conn;
    XplRWReadLockAcquire(&StoreAgent.configLock); {
        for (count = 0; count < StoreAgent.trustedHosts.count; count++) {
            if (client->conn->socketAddress.sin_addr.s_addr ==
                StoreAgent.trustedHosts.hosts[count]) 
            {
                client->flags |= STORE_CLIENT_FLAG_MANAGER;
                break;
            }
        }        
    } XplRWReadLockRelease(&StoreAgent.configLock);

    client->lockTimeoutMs = StoreAgent.store.lockTimeoutMs;
    
    BongoMemStackInit(&client->memstack, MEMSTACKSIZE);

    if (!ConnNegotiate(client->conn, StoreAgent.server.ssl.context)) {
        ccode = -1;
    } else if (!IS_MANAGER(client)) {
        /* issue nmap challenge */
        sprintf(client->authToken, "%x%s%x", 
                (unsigned int) XplGetThreadID(), 
                StoreAgent.server.host, 
                (unsigned int) time(NULL));
        ccode = ConnWriteF(client->conn, MSG4242AUTHREQUIRED, client->authToken);
    } else {
        ccode = ConnWriteF(client->conn, "1000 %s %s\r\n", 
                           StoreAgent.server.host, MSG1000READY);
    }
    if (ccode != -1) {
        ccode = ConnFlush(client->conn);
    }
    if (ccode != -1) {
        ccode = StoreCommandLoop(client);
    }

    /* Client connection will be freed by bongoagent's HandleConnection */
    
    return 0;
}


static int
StoreSocketInit()
{
    unsigned short port = BONGO_STORE_DEFAULT_PORT;

    // FIXME: previously, the port was configurable. However - we don't currently
    // have a way of communicating that to Store Agents

    StoreAgent.nmapConn = ConnAlloc(FALSE);
    if (!StoreAgent.nmapConn) {
        XplConsolePrintf(AGENT_NAME ": Could not allocate connection\n");
        return -1;
    }

    StoreAgent.nmapConn->socketAddress.sin_family = AF_INET;
    StoreAgent.nmapConn->socketAddress.sin_port = htons(port);
    StoreAgent.nmapConn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();
    
    /* Get root privs back for the bind.  It's ok if this fails - 
     * the user might not need to be root to bind to the port */
    XplSetEffectiveUserId(0);

    StoreAgent.nmapConn->socket = ConnServerSocket(StoreAgent.nmapConn, 2048);

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("bongostore: Could not drop to unprivileged user '%s'\n", 
                         MsgGetUnprivilegedUser());
        return -1;
    }

    if (StoreAgent.nmapConn->socket == -1) {
        int ret = StoreAgent.nmapConn->socket;
        XplConsolePrintf("bongostore: Could not bind to port %d\n", port);
        ConnFree(StoreAgent.nmapConn);
        return ret;
    }

    return 0;
}


static void 
StoreServer(void *ignored)
{
    XplRenameThread(XplGetThreadID(), AGENT_DN " Server");

    /* Listen for incoming connections.  Call ProcessEntry with a
     * StoreAgentClient allocated for each incoming queue entry. */
    BongoAgentListenWithClientPool(&StoreAgent.agent,
                                  StoreAgent.nmapConn,
                                  StoreAgent.threadPool,
                                  sizeof(StoreClient),
                                  StoreAgent.server.maxClients,
                                  StoreClientFree, 
                                  ProcessEntry,
                                  &StoreAgent.memPool);

#if VERBOSE
    /* Shutting down */
    XplConsolePrintf(AGENT_NAME ": Shutting down.\r\n");
#endif

    if (StoreAgent.nmapConn) {
        ConnClose(StoreAgent.nmapConn, 1);
        StoreAgent.nmapConn = NULL;
    }

    // StoreAgentManagementShutdown();

    BongoThreadPoolShutdown(StoreAgent.threadPool);
    CONN_TRACE_SHUTDOWN();
    BongoAgentShutdown(&StoreAgent.agent);
    
#if VERBOSE
    XplConsolePrintf(AGENT_NAME ": Shutdown complete\r\n");
#endif
}

#if defined(NETWARE) || defined(LIBC) || defined(WIN32)
int _NonAppCheckUnload(void)
{
    return(0);
}
#endif

static void 
SignalHandler(int sigtype) 
{
    BongoAgentHandleSignal(&StoreAgent.agent, sigtype);
}


XplServiceCode(SignalHandler)


int
_XplServiceMain(int argc, char *argv[])
{
    int ccode;
    int minThreads;
    int maxThreads;
    int minSleep;
    int i;
    BOOL cal_success;
    int startupOpts;
#ifdef WIN32
    XplThreadID id;
#endif
    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf("bongostore: Could not drop to unprivileged user '%s'\n", 
                         MsgGetUnprivilegedUser());
        return -1;
    }

    XplInit();
    strcpy(StoreAgent.nmapAddress, "127.0.0.1");

    StoreAgent.installMode = FALSE;
    for(i = 0; i < argc; i++) {
        if (!strncmp(argv[i], "--install", 9))
            StoreAgent.installMode = TRUE;
    }

    /* Initialize the Bongo libraries */
    startupOpts = BA_STARTUP_CONNIO;
    ccode = BongoAgentInit(&StoreAgent.agent, AGENT_NAME, AGENT_DN, (30 * 60), startupOpts);
    if (ccode == -1) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }
 
    if (! StoreAgent.installMode) {
        if (!(StoreAgent.handle.directory = (MDBHandle)MsgInit())) {
            XplBell();
            XplConsolePrintf("NMAPD: Invalid directory credentials; exiting!\r\n");
            XplBell();
            MemoryManagerClose(MSGSRV_AGENT_STORE);
            return -1;
        }
    
        MsgGetServerDN(StoreAgent.server.dn);
        cal_success = BongoCalInit(MsgGetDBFDir(NULL));
    } else {
        cal_success = BongoCalInit(XPL_DEFAULT_DBF_DIR);
    }
    
    if (! cal_success) {
        XplConsolePrintf(AGENT_NAME ": Couldn't initialize calendaring library.  Exiting.\r\n");
        return -1;
    }

    CONN_TRACE_INIT((char *)MsgGetWorkDir(NULL), "store");
    CONN_TRACE_SET_FLAGS(CONN_TRACE_ALL); /* uncomment this line and pass '--enable-conntrace' to autogen to get the agent to trace all connections */

    /* Create and bind the server connection */
    if (StoreSocketInit() < 0) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\n");
        return -1;
    }
    
    /* Drop privs */
    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf(AGENT_NAME ": Could not drop to unprivileged user '%s'\r\n"
                         AGENT_NAME ": exiting.\n", 
                         MsgGetUnprivilegedUser());
        return -1;
    }

    /* Create a thread pool for managing connections */

    BongoQueueAgentGetThreadPoolParameters (&StoreAgent.agent, &minThreads, 
                                          &maxThreads, &minSleep);
    
    StoreAgent.threadPool = BongoThreadPoolNew(AGENT_DN " Clients", 
                                              STACKSPACE,
                                              minThreads, maxThreads, minSleep);

    if (!StoreAgent.threadPool) {
        BongoAgentShutdown(&StoreAgent.agent);
        XplConsolePrintf(AGENT_NAME ": Unable to create thread pool.\r\n" 
                         AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    StoreAgentReadConfiguration(NULL);

    /* setup the store guts: */
    if (StoreSetupCommands()) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }
    
    if (DBPoolInit()) {
        XplConsolePrintf(AGENT_NAME ": Unable to create db pool.  Exiting.\r\n");
        return -1;
    }

    XplOpenLocalSemaphore(StoreAgent.guid.semaphore, 0);
    GuidReset();
    XplSignalLocalSemaphore(StoreAgent.guid.semaphore);
    NLockInit(8000);

    XplSignalHandler(SignalHandler);

    // GenericAgentManagementStart();
    
    /* Start the server thread */
    XplStartMainThread(AGENT_NAME, &id, StoreServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    
    return 0;
}
