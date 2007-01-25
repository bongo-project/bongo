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
#include <mdb.h>
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>
#include <streamio.h>

#include <curl/curl.h>

#include "collector.h"

#define TOK_ARR_SZ 10

CollectorGlobals Collector = {{0,}, 0, };

static void 
CollectorLoop(void *ignored)
{
    XplRenameThread(XplGetThreadID(), AGENT_DN);

    while (Collector.agent.state < BONGO_AGENT_STATE_STOPPING) {
        MsgCollectAllUsers();

        if (Collector.agent.state >= BONGO_AGENT_STATE_STOPPING) {
            break;
        }

        /* Refresh every 6 hours for now */
        sleep(60*60*6);
    }

#if VERBOSE
    /* Shutting down */
    XplConsolePrintf(AGENT_NAME ": Shutting down.\r\n");
#endif
}

static BOOL 
ReadConfiguration(void)
{
    MDBValueStruct *config;

    config = MDBCreateValueStruct(Collector.agent.directoryHandle, 
                                  MsgGetServerDN(NULL));
    if (config) {
        /* Read your agent's configuration here */
    } else {
        return FALSE;
    }

    MDBDestroyValueStruct(config);

    return TRUE;
}

static void 
SignalHandler(int sigtype) 
{
    BongoAgentHandleSignal(&Collector.agent, sigtype);
}

XplServiceCode(SignalHandler)

int
XplServiceMain(int argc, char *argv[])
{
    int ccode;

    if (XplSetRealUser(MsgGetUnprivilegedUser()) < 0) {
        XplConsolePrintf(AGENT_NAME ": Could not drop to unprivileged user '%s'\r\n" AGENT_NAME ": exiting.\n", MsgGetUnprivilegedUser());
        return -1;
    }
    XplInit();

    /* Initialize the Bongo libraries */
    ccode = BongoAgentInit(&Collector.agent, AGENT_NAME, AGENT_DN, DEFAULT_CONNECTION_TIMEOUT);
    if (ccode == -1) {
        XplConsolePrintf(AGENT_NAME ": Exiting.\r\n");
        return -1;
    }

    ReadConfiguration();

    XplSignalHandler(SignalHandler);

    CollectorManagementStart();

    MsgInit();
    StreamioInit();
    ConnStartup(DEFAULT_CONNECTION_TIMEOUT, TRUE);

    /* Start the collector thread */
    XplStartMainThread(AGENT_NAME, &colid, CollectorLoop, 8192, NULL, ccode);
    
    XplUnloadApp(XplGetThreadID());
    
    return 0;
}
