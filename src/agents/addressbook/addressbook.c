/****************************************************************************
 *  * <Novell-copyright>
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

#include "addressbook.h"
#include "../store/messages.h"

#define STACKSPACE (128 * 1024)

#define MEMSTACKSIZE (4 * 1024)

struct AddressbookGlobals AddressbookAgent;

static void 
AddressbookClientFree(void *clientp)
{
    AddressbookClient *client = clientp;

    MemPrivatePoolReturnEntry(client);
}

static int 
ProcessEntry(void *clientp,
             Connection *conn)
{
    int ccode;
    int count;
    AddressbookClient *client;

    client = clientp;
    client->conn = conn;
    XplRWReadLockAcquire(&AddressbookAgent.configLock); {
        for (count = 0; count < AddressbookAgent.trustedHosts.count; count++) {
            if (client->conn->socketAddress.sin_addr.s_addr ==
                AddressbookAgent.trustedHosts.hosts[count]) 
            {
                client->flags |= ADDRESSBOOK_CLIENT_FLAG_MANAGER;
                break;
            }
        }        
    } XplRWReadLockRelease(&AddressbookAgent.configLock);

    if (!ConnNegotiate(client->conn, AddressbookAgent.server.ssl.context)) {
        ccode = -1;
    } else if (!IS_MANAGER(client)) {
        /* issue nmap challenge */
        sprintf(client->authToken, "%x%s%x", 
                (unsigned int) XplGetThreadID(), 
                AddressbookAgent.server.host, 
                (unsigned int) time(NULL));
        ccode = ConnWriteF(client->conn, MSG4242AUTHREQUIRED, client->authToken);
    } else {
        ccode = ConnWriteF(client->conn, "1000 %s %s\r\n", 
                           AddressbookAgent.server.host, MSG1000READY);
    }
    if (ccode != -1) {
        ccode = ConnFlush(client->conn);
    }
    if (ccode != -1) {
        ccode = AddressbookCommandLoop(client);
    }

    /* Client connection will be freed by bongoagent's HandleConnection */
    
    return 0;
}


static int
AddressbookSocketInit(void)
{
    MDBValueStruct *vs;
    unsigned short port = 8672;

    vs = MDBCreateValueStruct(AddressbookAgent.handle.directory, AddressbookAgent.server.dn);
    if (vs) {
        if (MDBRead(AGENT_DN, MSGSRV_A_PORT, vs) > 0) {
            port = atol(vs->Value[0]);
            MDBFreeValues(vs);
        }
        MDBDestroyValueStruct(vs);
    }

    AddressbookAgent.listenConn = ConnAlloc(FALSE);
    if (!AddressbookAgent.listenConn) {
        XplConsolePrintf(AGENT_NAME ": Could not allocate connection\n");
        return -1;
    }
    
    AddressbookAgent.listenConn->socketAddress.sin_family = AF_INET;
    AddressbookAgent.listenConn->socketAddress.sin_port = htons(port);
    AddressbookAgent.listenConn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();

    /* Get root privs back for the bind.  It's ok if this fails - 
     * the user might not need to be root to bind to the port */
    XplSetEffectiveUserId(0);

    AddressbookAgent.listenConn->socket = ConnServerSocket(AddressbookAgent.listenConn, 2048);

    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf(AGENT_NAME ": Could not drop to unprivileged user '%s'\n", 
                         MsgGetUnprivilegedUser());
        return -1;
    }

    if (AddressbookAgent.listenConn->socket == -1) {
        int ret = AddressbookAgent.listenConn->socket;
        XplConsolePrintf(AGENT_NAME ": Could not bind to port %d\n", port);
        ConnFree(AddressbookAgent.listenConn);
        return ret;
    }

    return 0;
}


static void 
AddressbookServer(void *ignored)
{
    XplRenameThread(XplGetThreadID(), AGENT_DN " Server");

    /* Listen for incoming connections.  Call ProcessEntry with a
     * AddressbookAgentClient allocated for each incoming queue entry. */
    BongoAgentListenWithClientPool(&AddressbookAgent.agent,
                                  AddressbookAgent.listenConn,
                                  AddressbookAgent.threadPool,
                                  sizeof(AddressbookClient),
                                  AddressbookAgent.server.maxClients,
                                  AddressbookClientFree, 
                                  ProcessEntry,
                                  &AddressbookAgent.memPool);

    if (AddressbookAgent.listenConn) {
        ConnClose(AddressbookAgent.listenConn, 1);
        AddressbookAgent.listenConn = NULL;
    }

    BongoThreadPoolShutdown(AddressbookAgent.threadPool);
    CONN_TRACE_SHUTDOWN();
    BongoAgentShutdown(&AddressbookAgent.agent);
}


static BOOL
AddressbookAgentReadConfiguration(BOOL *recover)
{
    unsigned long used;
    
    BOOL result = TRUE;
    MDBValueStruct *vs;
    
    tzset();
    
    vs = MDBCreateValueStruct(AddressbookAgent.handle.directory, NULL);
    if (vs && MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, vs)) {
        gethostname(AddressbookAgent.server.host, sizeof(AddressbookAgent.server.host));
        
        HashCredential(MsgGetServerDN(NULL), vs->Value[0], AddressbookAgent.server.hash);
        
        MDBFreeValues(vs);
    } else {
        return(FALSE);
    }
    
    if (recover) {
        if (MDBRead(AddressbookAgent.server.dn, MSGSRV_A_SERVER_STATUS, vs) > 0) {
            if (XplStrCaseCmp(vs->Value[0], "Shutdown") != 0) {
                *recover = TRUE;
            }
        }
        
        MDBFreeValues(vs);
    }
    
    MDBSetValueStructContext(AddressbookAgent.server.dn, vs);
    
    /* trusted hosts */
    AddressbookAgent.trustedHosts.count = 
        MDBRead(MSGSRV_AGENT_STORE, MSGSRV_A_STORE_TRUSTED_HOSTS, vs);
    
    AddressbookAgent.trustedHosts.hosts = MemRealloc(AddressbookAgent.trustedHosts.hosts, 
                                               AddressbookAgent.trustedHosts.count + 2);
    if (!AddressbookAgent.trustedHosts.hosts) {
        AddressbookAgent.trustedHosts.count = 0;
    } 
    if (AddressbookAgent.trustedHosts.count) {
        int count = 0;
        
#if defined(NETWARE) || defined(LIBC)
        AddressbookAgent.trustedHosts.hosts[count++] = inet_addr("127.0.0.1");
        AddressbookAgent.trustedHosts.hosts[count++] = MsgGetHostIPAddress();
#endif
        for (used = 0; used < vs->Used; used++) {
            AddressbookAgent.trustedHosts.hosts[count++] = inet_addr(vs->Value[used]);
        }
    }
    MDBFreeValues(vs);
    /* end trusted hosts */
    
    
    /** hacks **/
    
    AddressbookAgent.server.maxClients = 1024;
    
    /** end hacks **/

    
    MDBDestroyValueStruct(vs);
    
    return(result);
}


static void 
SignalHandler(int sigtype) 
{
    BongoAgentHandleSignal(&AddressbookAgent.agent, sigtype);
}

XplServiceCode(SignalHandler)


int
XplServiceMain(int argc, char *argv[])
{
    int ccode;
    int minThreads;
    int maxThreads;
    int minSleep;
    int startupOpts = 0;
#ifdef WIN32
    XplThreadID id;
#endif
    if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf(AGENT_NAME ": Could not drop to unprivileged user '%s'\n", 
                         MsgGetUnprivilegedUser());
        return -1;
    }
    XplInit();

    /* Initialize the Bongo libraries */
    startupOpts = BA_STARTUP_MDB | BA_STARTUP_CONNIO;
    ccode = BongoAgentInit(&AddressbookAgent.agent, AGENT_NAME, AGENT_DN, (30 * 60), startupOpts);
    if (ccode == -1) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    if (!BongoCalInit(MsgGetDBFDir(NULL))) {
        XplConsolePrintf(AGENT_NAME ": Couldn't initialize calendaring library.  Exiting.\r\n");
        return -1;
    }

    MsgGetServerDN(AddressbookAgent.server.dn);
    MsgGetConfigProperty(AddressbookAgent.defaultContext, MSGSRV_CONFIG_PROP_DEFAULT_CONTEXT);    

    /* Create and bind the server connection */
    if (AddressbookSocketInit() < 0) {
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

    BongoQueueAgentGetThreadPoolParameters (&AddressbookAgent.agent, &minThreads, 
                                          &maxThreads, &minSleep);
    
    AddressbookAgent.threadPool = BongoThreadPoolNew(AGENT_DN " Clients", 
                                              STACKSPACE,
                                              minThreads, maxThreads, minSleep);

    if (!AddressbookAgent.threadPool) {
        BongoAgentShutdown(&AddressbookAgent.agent);
        XplConsolePrintf(AGENT_NAME ": Unable to create thread pool.\r\n" 
                         AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    AddressbookAgentReadConfiguration(NULL);

    /* setup the guts: */
    if (AddressbookSetupCommands()) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    XplSignalHandler(SignalHandler);

    /* Start the server thread */
    XplStartMainThread(AGENT_NAME, &id, AddressbookServer, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    
    return 0;
}
