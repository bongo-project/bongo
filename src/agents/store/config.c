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
#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include <connio.h>

#include "stored.h"

BOOL
StoreAgentReadConfiguration(BOOL *recover)
{
    tzset();
    StoreAgent.startupTime = time(NULL);

    StoreAgent.store.incomingQueueBytes = 1024 * 1024;
    StoreAgent.store.lockTimeoutMs = 10000;
    
    /* FIXME: tweak this */
    StoreAgent.dbpool.capacity = 64;

    // this is apparently a hack. Perhaps this should be configurable?
    StoreAgent.server.maxClients = 1024;

    // paths etc. now fixed in code - if we want to configure these, we need a new system :)
    strcpy(StoreAgent.store.rootDir, XPL_DEFAULT_MAIL_DIR);
    strcpy(StoreAgent.store.systemDir, XPL_DEFAULT_STORE_SYSTEM_DIR);
    strcpy(StoreAgent.store.spoolDir, XPL_DEFAULT_SPOOL_DIR);
    MsgMakePath(StoreAgent.store.rootDir);
    MsgMakePath(StoreAgent.store.systemDir);
    MsgMakePath(StoreAgent.store.spoolDir);

    // which hosts do we trust? SECURITY: This bypasses auth!
    StoreAgent.trustedHosts.count = 0;
#if 0
    StoreAgent.trustedHosts.count = 2;
    StoreAgent.trustedHosts.hosts = 
        MemRealloc(StoreAgent.trustedHosts.hosts, sizeof(unsigned long) * 2);
    if (!StoreAgent.trustedHosts.hosts) {
        StoreAgent.trustedHosts.count = 0;
    } else {
        StoreAgent.trustedHosts.hosts[0] = inet_addr("127.0.0.1");
        StoreAgent.trustedHosts.hosts[1] = MsgGetHostIPAddress(); // do we need this address too?
    }
#endif
    
    gethostname(StoreAgent.server.host, sizeof(StoreAgent.server.host));
    if (!MsgGetServerCredential(StoreAgent.server.hash))
        // can't proceed without the server hash..
        return(FALSE);
{
        char buffer[10000];
        int i;
        memset(&buffer, 0, 100);
        MsgAlex(&buffer);
        XplConsolePrintf("Mine: ", buffer);
        for(i = 0; i < 20; i++) {
                XplConsolePrintf("%x", buffer[i]);
        }
        XplConsolePrintf("\n");
        memset(&buffer, 0, 100);
        MsgGetServerCredential(&buffer);
        XplConsolePrintf("Orig: ", buffer);
        for(i = 0; i < 20; i++) {
                XplConsolePrintf("%x", buffer[i]);
        }
        XplConsolePrintf("\n");
}

    if (recover && MsgGetRecoveryFlag()) {
        *recover = TRUE;
    }

    return(TRUE);
}
