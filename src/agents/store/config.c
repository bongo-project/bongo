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

BOOL
StoreAgentReadConfiguration(BOOL *recover)
{
    unsigned long used;

    BOOL result = TRUE;
    MDBValueStruct *vs;
    MDBValueStruct *stores;

    tzset();
    StoreAgent.startupTime = time(NULL);

    vs = MDBCreateValueStruct(StoreAgent.handle.directory, NULL);
    if (vs && MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, vs)) {
        gethostname(StoreAgent.server.host, sizeof(StoreAgent.server.host));

        HashCredential(MsgGetServerDN(NULL), vs->Value[0], StoreAgent.server.hash);

        MDBFreeValues(vs);
    } else {
        return(FALSE);
    }

    if (recover) {
        if (MDBRead(StoreAgent.server.dn, MSGSRV_A_SERVER_STATUS, vs) > 0) {
            if (XplStrCaseCmp(vs->Value[0], "Shutdown") != 0) {
                *recover = TRUE;
            }
        }

        MDBFreeValues(vs);
    }

    MDBSetValueStructContext(StoreAgent.server.dn, vs);

    /* trusted hosts */
    StoreAgent.trustedHosts.count = 
        MDBRead(MSGSRV_AGENT_STORE, MSGSRV_A_STORE_TRUSTED_HOSTS, vs);

    StoreAgent.trustedHosts.hosts = MemRealloc(StoreAgent.trustedHosts.hosts, 
                                               StoreAgent.trustedHosts.count + 2);
    if (!StoreAgent.trustedHosts.hosts) {
        StoreAgent.trustedHosts.count = 0;
    } 
    if (StoreAgent.trustedHosts.count) {
        int count = 0;

#if defined(NETWARE) || defined(LIBC)
        StoreAgent.trustedHosts.hosts[count++] = inet_addr("127.0.0.1");
        StoreAgent.trustedHosts.hosts[count++] = MsgGetHostIPAddress();
#endif
        for (used = 0; used < vs->Used; used++) {
            StoreAgent.trustedHosts.hosts[count++] = inet_addr(vs->Value[used]);
        }
    }
    MDBFreeValues(vs);
    /* end trusted hosts */


    /** hacks **/
    
    StoreAgent.server.maxClients = 1024;

    /** end hacks **/


    if (MDBRead(MSGSRV_AGENT_STORE, MSGSRV_A_MESSAGE_STORE, vs) > 0) {
        LoggerEvent(StoreAgent.handle.logging, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_MESSAGE_STORE", vs->Value[0], 0, 0, NULL, 0);

        MsgCleanPath(vs->Value[0]);

        strcpy(StoreAgent.store.rootDir, vs->Value[0]);
        MsgMakePath(StoreAgent.store.rootDir);

        MDBFreeValues(vs);
    } else {
        result = FALSE;
        strcpy(StoreAgent.store.rootDir, XPL_DEFAULT_MAIL_DIR);
    }

    MsgMakePath(StoreAgent.store.rootDir);

    /* Create Store Paths if they don't exist */
    if (MDBReadDN(StoreAgent.server.dn, MSGSRV_A_CONTEXT, vs) > 0) {
        stores = MDBCreateValueStruct(StoreAgent.handle.directory, NULL);
        if (stores) {
            for (used = 0; used < vs->Used; used++) {
                if (MDBRead(vs->Value[used], MSGSRV_A_MESSAGE_STORE, stores) > 0) {
                    MsgCleanPath(stores->Value[0]);
                    MsgMakePath(stores->Value[0]);

                    MDBFreeValues(stores);            
                }
            }

            MDBDestroyValueStruct(stores);
        } else {
            result = FALSE;
        }

        MDBFreeValues(vs);
    }

    strcpy(StoreAgent.store.systemDir, XPL_DEFAULT_STORE_SYSTEM_DIR);
    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_STORE_SYSTEM_DIRECTORY, vs) > 0) {
        LoggerEvent(StoreAgent.handle.logging, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_STORE_SYSTEM_DIRECTORY", vs->Value[0], 0, 0, NULL, 0);
        
        MsgCleanPath(vs->Value[0]);        
        strcpy(StoreAgent.store.systemDir, vs->Value[0]);
        MsgMakePath(StoreAgent.store.systemDir);

        MDBFreeValues(vs);
    }

    strcpy(StoreAgent.store.spoolDir, XPL_DEFAULT_SPOOL_DIR);
    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_SPOOL_DIRECTORY, vs) > 0) {
        LoggerEvent(StoreAgent.handle.logging, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_SPOOL_DIRECTORY", vs->Value[0], 0, 0, NULL, 0);
        
        MsgCleanPath(vs->Value[0]);        
        strcpy(StoreAgent.store.spoolDir, vs->Value[0]);
        MsgMakePath(StoreAgent.store.spoolDir);

        MDBFreeValues(vs);
    }

    StoreAgent.store.incomingQueueBytes = 1024 * 1024;
    StoreAgent.store.lockTimeoutMs = 10000;
    
    /* FIXME: tweak this */
    StoreAgent.dbpool.capacity = 64;

    MDBDestroyValueStruct(vs);

    return(result);
}
