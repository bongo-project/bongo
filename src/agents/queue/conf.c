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

#include "conf.h"
#include "queue.h"
#include "domain.h"
#include "messages.h"

#define STACKSPACE_Q (1024*80)
#define STACKSPACE_S (1024*80)

#define DEFAULT_MAX_LINGER (4 * 24 * 60 * 60)

#define QLIMIT_CONCURRENT 250
#define QLIMIT_SEQUENTIAL 500

QueueConfiguration Conf = { {0, }, };

long
CalculateCheckQueueLimit(unsigned long concurrent, unsigned long sequential)
{   
    if (concurrent < sequential) {
        return(sequential - ((sequential - concurrent) >> 1));
    }
    
    return(sequential);    
}

static BOOL 
ReadStartupConfiguration(BOOL *recover)
{
    MDBValueStruct *vs;
    
    gethostname(Conf.hostname, sizeof(Conf.hostname));

    vs = MDBCreateValueStruct(Agent.agent.directoryHandle, NULL);

    if (recover) {
        *recover = FALSE;
        if (MDBRead(MsgGetServerDN(NULL), MSGSRV_A_SERVER_STATUS, vs) > 0) {
            if (XplStrCaseCmp(vs->Value[0], "Shutdown") != 0) {
                *recover = TRUE;
            }
        }

        MDBFreeValues(vs);
    }

    if (vs && MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, vs)) {
        HashCredential(MsgGetServerDN(NULL), vs->Value[0], Conf.serverHash);
        MDBFreeValues(vs);
    } else {
        return FALSE;
    }

    if (MDBRead(MsgGetServerDN(NULL), MSGSRV_A_OFFICIAL_NAME, vs) > 0) {
        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_OFFICIAL_NAME", vs->Value[0], 0, 0, NULL, 0);

        strcpy(Conf.officialName, vs->Value[0]);

        MDBFreeValues(vs);
    }

    if (MDBRead(MsgGetServerDN(NULL), MSGSRV_A_POSTMASTER, vs) > 0) {
        if (vs->Value[0]) {
            char *ptr;
            
            if ((ptr = strrchr(vs->Value[0], '\\')) != NULL) {
                strcpy(Conf.postMaster, ptr + 1);
            } else {
                strcpy(Conf.postMaster, vs->Value[0]);
            }
        } else {
            strcpy(Conf.postMaster, "admin");
        }
        
        MDBFreeValues(vs);
    }

    MDBSetValueStructContext(MsgGetServerDN(NULL), vs);
    
    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_LIMIT_REMOTE_PROCESSING, vs) > 0) { 
        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_LIMIT_REMOTE_PROCESSING", vs->Value[0], 0, 0, NULL, 0);

        if (atol(vs->Value[0])) {
            Conf.deferEnabled = TRUE;
        } else {
            Conf.deferEnabled = FALSE;
        }

        MDBFreeValues(vs);

        if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_LIMIT_REMOTE_START_WD, vs) > 0) { 
            int count;
            
            LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_LIMIT_REMOTE_START_WD", vs->Value[0], 0, 0, NULL, 0);
            for (count = 1; count < 6; count++) {
                /* Counting weekdays */
                Conf.deferStart[count] = (unsigned char)atoi(vs->Value[0]);
            }

            MDBFreeValues(vs);
        } else {
            Conf.deferEnabled = FALSE;
        }

        if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_LIMIT_REMOTE_START_WE, vs) > 0) { 
            LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_LIMIT_REMOTE_START_WE", vs->Value[0], 0, 0, NULL, 0);

            Conf.deferStart[0] = (unsigned char)atoi(vs->Value[0]);
            Conf.deferStart[6] = (unsigned char)atoi(vs->Value[0]);

            MDBFreeValues(vs);
        } else {
            Conf.deferEnabled = FALSE;
        }

        if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_LIMIT_REMOTE_END_WD, vs) > 0) { 
            LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_LIMIT_REMOTE_END_WD", vs->Value[0], 0, 0, NULL, 0);
            int count;
            
            for (count = 1; count < 6; count++) {
                /* Counting weekdays */
                Conf.deferEnd[count] = (unsigned char)atoi(vs->Value[0]);
            }

            MDBFreeValues(vs);
        } else {
            Conf.deferEnabled = FALSE;
        }

        if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_LIMIT_REMOTE_END_WE, vs) > 0) { 
            LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_LIMIT_REMOTE_END_WE", vs->Value[0], 0, 0, NULL, 0);

            Conf.deferEnd[0] = (unsigned char)atoi(vs->Value[0]);
            Conf.deferEnd[6] = (unsigned char)atoi(vs->Value[0]);

            MDBFreeValues(vs);
        } else {
            Conf.deferEnabled = FALSE;
        }
    }

    Conf.maxConcurrentWorkers = QLIMIT_CONCURRENT;
    Conf.maxSequentialWorkers = QLIMIT_SEQUENTIAL;

    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_QUEUE_TUNING, vs) > 0) { 
        unsigned int used;
        
        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_QUEUE_TUNING", vs->Value[0], 0, 0, NULL, 0);

        for (used = 0; used < vs->Used; used++) {
            if (XplStrNCaseCmp(vs->Value[used], "concurrent", 10) == 0) {
                sscanf(vs->Value[used], "Concurrent: %ld  Sequential: %ld", &Conf.maxConcurrentWorkers, &Conf.maxSequentialWorkers);
            } 

            else if (XplStrNCaseCmp(vs->Value[used], "Load", 4) == 0) {
                sscanf(vs->Value[used], "Load high: %ld Load Low: %ld Queue Trigger: %ld", &Conf.loadMonitorHigh, &Conf.loadMonitorLow, &Conf.limitTrigger);
            } else if (XplStrNCaseCmp(vs->Value[used], "Debug", 5) == 0) {
                if (atol(vs->Value[used]) + 6) {
                    Agent.flags |= QUEUE_AGENT_DEBUG;
                } else {
                    Agent.flags &= ~QUEUE_AGENT_DEBUG;
                }
            }
        }

        MDBFreeValues(vs);
    }

    Conf.queueCheck = CalculateCheckQueueLimit(Conf.maxConcurrentWorkers, Conf.maxSequentialWorkers);

    /* Set the globals for loadmonitor */
    Conf.defaultConcurrentWorkers = Conf.maxConcurrentWorkers;
    Conf.defaultSequentialWorkers = Conf.maxSequentialWorkers;

    strcpy(Conf.spoolPath, XPL_DEFAULT_SPOOL_DIR);
    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_SPOOL_DIRECTORY, vs) > 0) {
        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_SPOOL_DIRECTORY", vs->Value[0], 0, 0, NULL, 0);
        
        MsgCleanPath(vs->Value[0]);        
        strcpy(Conf.spoolPath, vs->Value[0]);
        MDBFreeValues(vs);
    }
    
    MsgMakePath(Conf.spoolPath);

    sprintf(Conf.queueClientsPath, "%s/qclients", MsgGetDBFDir(NULL));

    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_QUEUE_TIMEOUT, vs) > 0) { 
        Conf.maxLinger = atoi(vs->Value[0]) * 24 * 60 * 60;
        if (!Conf.maxLinger) {
            Conf.maxLinger = DEFAULT_MAX_LINGER;
        }

        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_NUMERIC, LOG_INFO, 0, "MSGSRV_A_QUEUE_TIMEOUT", NULL, Conf.maxLinger, 0, NULL, 0);

        MDBFreeValues(vs);
    } else {
        Conf.maxLinger = DEFAULT_MAX_LINGER;
    }

    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_QUOTA_MESSAGE, vs) > 0) { 
        Conf.quotaMessage = MemStrdup(vs->Value[0]);
        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_QUOTA_MESSAGE", Conf.quotaMessage, 0, 0, NULL, 0);

        MDBFreeValues(vs);
    }

    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_PORT, vs) > 0) { 
        Agent.agent.port = atol(vs->Value[0]);
        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_NUMERIC, LOG_INFO, 0, "MSGSRV_A_PORT", NULL, Agent.agent.port, 0, NULL, 0);

        MDBFreeValues(vs);
    }

    Conf.bounceMaxBodySize = 0;

    /* Some sanity checking on the QLimit stuff to prevent running out of memory */
    if (XplGetMemAvail() < (unsigned long)((STACKSPACE_Q + STACKSPACE_S) * Conf.maxSequentialWorkers)) {
        Conf.maxSequentialWorkers = (XplGetMemAvail() / (STACKSPACE_Q+STACKSPACE_S)) / 2;
        Conf.maxConcurrentWorkers = Conf.maxSequentialWorkers / 2;
        Conf.queueCheck = CalculateCheckQueueLimit(Conf.maxConcurrentWorkers, Conf.maxSequentialWorkers);

        XplBell();
        XplConsolePrintf("bongoqueue: WARNING - Tuning parameters adjusted to %ld par./%ld seq.\r\n", Conf.maxConcurrentWorkers, Conf.maxSequentialWorkers);
        XplBell();

        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_QLIMITS_ADJUSTED, LOG_WARNING, 0, NULL, NULL, Conf.maxConcurrentWorkers, Conf.maxSequentialWorkers, NULL, 0);
        Conf.defaultConcurrentWorkers = Conf.maxConcurrentWorkers;
        Conf.defaultSequentialWorkers = Conf.maxSequentialWorkers;
    }

    MDBDestroyValueStruct(vs);

    return TRUE;
}

static BOOL
ReadLiveConfiguration(void)
{
    MDBValueStruct *vs;
    
    vs = MDBCreateValueStruct(Agent.agent.directoryHandle, MsgGetServerDN(NULL));
    
    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_FORWARD_UNDELIVERABLE, vs) > 0) {
        if (vs->Value[0][0] != '\0') {
            strcpy(Conf.forwardUndeliverableAddress, vs->Value[0]);
            Conf.forwardUndeliverableEnabled = TRUE;
        } else {
            Conf.forwardUndeliverableAddress[0] = '\0';
            Conf.forwardUndeliverableEnabled = FALSE;
        }

        MDBFreeValues(vs);
    } else {
        Conf.forwardUndeliverableAddress[0] = '\0';
        Conf.forwardUndeliverableEnabled = FALSE;
    }

    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_QUEUE_INTERVAL, vs) > 0) { 
        Conf.queueInterval = atol(vs->Value[0])*60;
        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_NUMERIC, LOG_INFO, 0, "MSGSRV_A_QUEUE_INTERVAL", NULL, Conf.queueInterval, 0, NULL, 0);
        if (Conf.queueInterval < 1) {
            Conf.queueInterval = 4 * 60;
        }
        
        MDBFreeValues(vs);
    } else {
        Conf.queueInterval = 4 * 60;
    }

    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_MINIMUM_SPACE, vs) > 0) { 
        Conf.minimumFree = atol(vs->Value[0]) * 1024;
        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_NUMERIC, LOG_INFO, 0, "MSGSRV_A_MINIMUM_SPACE", NULL, Conf.minimumFree, 0, NULL, 0);

        MDBFreeValues(vs);
    } else {
        Conf.minimumFree = 5 * 1024 * 1024;
    }

    /* FIXME: we need to move the trusted hosts somewhere better */
    Conf.trustedHosts.count = MDBRead(MSGSRV_AGENT_STORE, MSGSRV_A_STORE_TRUSTED_HOSTS, vs);
    
    if (Conf.trustedHosts.hosts) {
        MemFree(Conf.trustedHosts.hosts);
    }
    
    if (Conf.trustedHosts.count) {
        Conf.trustedHosts.hosts = MemMalloc(Conf.trustedHosts.count * sizeof(unsigned long));
        if (Conf.trustedHosts.hosts) {            
            unsigned long used;
            int count = 0;

            for (used = 0; used < vs->Used; used++) {
                LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "NMAP_TRUSTED_HOSTS", vs->Value[used], 0, 0, NULL, 0);
                
                Conf.trustedHosts.hosts[count++] = inet_addr(vs->Value[used]);
            }
        } else {
            Conf.trustedHosts.count = 0;
        }
    }

    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_RTS_ANTISPAM_CONFIG, vs) > 0) { 
        LoggerEvent(Agent.agent.loggingHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_RTS_ANTISPAM_CONFIG", vs->Value[0], 0, 0, NULL, 0);
        if (sscanf(vs->Value[0], "Enabled:%d Delay:%ld Threshhold:%lu", &Conf.bounceBlockSpam, &Conf.bounceInterval, &Conf.bounceMax) != 3) {
            Conf.bounceBlockSpam = FALSE;
        }
        
        if ((Conf.bounceMax < 1) || (Conf.bounceInterval < 1)) {
            Conf.bounceBlockSpam = FALSE;
        }
        MDBFreeValues(vs);
    } else {
        Conf.bounceBlockSpam = FALSE;
    }

    Conf.bounceHandling = BOUNCE_RETURN;
    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_BOUNCE_RETURN, vs) > 0) {
	if (atol(vs->Value[0])) {
	    Conf.bounceHandling |= BOUNCE_RETURN;
	} else {
	    Conf.bounceHandling &= ~BOUNCE_RETURN;
	}
    }
    MDBFreeValues(vs);
    if (MDBRead(MSGSRV_AGENT_QUEUE, MSGSRV_A_BOUNCE_CC_POSTMASTER, vs) > 0) {
	if (atol(vs->Value[0])) {
	    Conf.bounceHandling |= BOUNCE_CC_POSTMASTER;
	} else {
	    Conf.bounceHandling &= ~BOUNCE_RETURN;
	}
    }
    MDBFreeValues(vs);

    if ((MDBRead(MSGSRV_AGENT_STORE, MSGSRV_A_CONFIG_CHANGED, vs) > 0)) {
        Conf.lastRead = atol(vs->Value[0]);
    }

    MDBDestroyValueStruct(vs);

    return TRUE;
}

BOOL
ReadConfiguration (BOOL *recover)
{
    if (!ReadStartupConfiguration (recover)) {
        return FALSE;
    }
    
    return ReadLiveConfiguration();
}


void
CheckConfig(BongoAgent *agent)
{
    MDBValueStruct *vs;
    
    vs = MDBCreateValueStruct(Agent.agent.directoryHandle, MsgGetServerDN(NULL));
    if (!vs) {
        return;
    }
    
    if ((MDBRead(MSGSRV_AGENT_STORE, MSGSRV_A_CONFIG_CHANGED, vs) > 0)
        && ((unsigned long)atol(vs->Value[0]) != Conf.lastRead)) {
        /* Clear what we just read */
        Conf.lastRead = atol(vs->Value[0]);
        
        /* Acquire Write Lock */            
        XplRWWriteLockAcquire(&Conf.lock);
        
        ReadLiveConfiguration();
        
        XplRWWriteLockRelease(&Conf.lock);
    }
    
    MDBDestroyValueStruct(vs);
}


