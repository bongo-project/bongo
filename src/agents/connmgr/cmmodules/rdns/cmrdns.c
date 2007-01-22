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
#include <xplresolve.h>
#include <mdb.h>
#include <msgapi.h>

#include <sqlite3.h>

#include "cmrdns.h"

/* Globals */
BOOL RDNSUnloadOk = TRUE;
RDNSGlobal RDNS;

static BOOL ReadConfiguration(void);


EXPORT int RDNSVerify(ConnMgrCommand *command, ConnMgrResult *result)
{
    if (XplStrCaseCmp(command->event, CM_EVENT_CONNECT) == 0) {
        XplDnsRecord *dns = NULL;
        BOOL blocked = FALSE;
        int rc = -1;

        /* Check for a cached value */
        XplWaitOnLocalSemaphore(RDNS.sqlite.semaphore);

        sqlite3_bind_int(RDNS.sqlite.stmt.get, 1, command->address);

        if ((rc = sqlite3_step(RDNS.sqlite.stmt.get)) == SQLITE_ROW) {
            blocked = sqlite3_column_int(RDNS.sqlite.stmt.get, 0);
        }

        sqlite3_reset(RDNS.sqlite.stmt.get);
        XplSignalLocalSemaphore(RDNS.sqlite.semaphore);

        if (rc != SQLITE_ROW) {
            unsigned char buffer[MAXEMAILNAMESIZE];
            struct in_addr ip;

            /* Perform the RDNS check */

            ip.s_addr = command->address;

            /* ip is in host byte order.  Reverse DNS requires most significant first.  (unless providing the .in-addr.arpa extension) */
            snprintf(buffer, sizeof(buffer), "%d.%d.%d.%d",
                ip.s_impno,
                ip.s_lh,
                ip.s_host,
                ip.s_net);

            rc = XplDnsResolve(buffer, &dns, XPL_RR_PTR);

            if (dns) {
                MemFree(dns);
            }

            if (rc != XPL_DNS_SUCCESS) {
                /* We don't like the guy */
                blocked = TRUE;
            }

            /* Store new value */
            XplWaitOnLocalSemaphore(RDNS.sqlite.semaphore);

            /*
                Insert the new value into the table.  The time for a negative result is a
                full day.  The time for a positive result is only 15 minutes however,
                because a positive result simply means that the servers do not know anything
                about the address.
            */
            sqlite3_bind_int(RDNS.sqlite.stmt.add, 1, command->address);
            sqlite3_bind_text(RDNS.sqlite.stmt.add, 2, blocked ? RDNS.config.blockTimeout : RDNS.config.allowTimeout, -1, SQLITE_TRANSIENT);
            sqlite3_bind_int(RDNS.sqlite.stmt.add, 3, blocked);

            sqlite3_step(RDNS.sqlite.stmt.add);

            sqlite3_reset(RDNS.sqlite.stmt.add);
            XplSignalLocalSemaphore(RDNS.sqlite.semaphore);
        }

        if (blocked) {
            /* He was blocked by RDNS */
            strncpy(result->comment, CM_COMMENT, sizeof(result->comment));
        }

        result->detail.connect.requireAuth = blocked;
        return(CM_MODULE_ACCEPT);
    }

    return(0);
}

static void
TransactionController(void *ignored)
{
    XplWaitOnLocalSemaphore(RDNS.sqlite.semaphore);
    if (sqlite3_exec(RDNS.sqlite.handle, SQL_BEGIN, NULL, NULL, NULL) != SQLITE_OK) {
        XplConsolePrintf("cmrdns: Failed to start transaction controller\n");
    }
    XplSignalLocalSemaphore(RDNS.sqlite.semaphore);

    while (!RDNSUnloadOk) {
        unsigned long i;

        for (i = 0; i < 15 && !RDNSUnloadOk; i++) {
            XplDelay(1000);
        }

        XplWaitOnLocalSemaphore(RDNS.sqlite.semaphore);
        if (sqlite3_exec(RDNS.sqlite.handle, SQL_SAVE, NULL, NULL, NULL) != SQLITE_OK) {
            XplConsolePrintf("cmrdns: Failed to save cache data\n");
        }
        XplSignalLocalSemaphore(RDNS.sqlite.semaphore);
    }

    XplWaitOnLocalSemaphore(RDNS.sqlite.semaphore);
    if (sqlite3_exec(RDNS.sqlite.handle, SQL_END, NULL, NULL, NULL) != SQLITE_OK) {
        XplConsolePrintf("cmrdns: Failed to end transaction controller\n");
    }
    XplSignalLocalSemaphore(RDNS.sqlite.semaphore);
}

EXPORT BOOL
CMRDNSInit(CMModuleRegistrationStruct *registration, unsigned char *datadir)
{
    if (RDNSUnloadOk == TRUE) {
        XplSafeWrite(RDNS.threadCount, 0);

		RDNS.directoryHandle = (MDBHandle) MsgInit();
        if (RDNS.directoryHandle) {
            RDNSUnloadOk = FALSE;

            RDNS.logHandle = LoggerOpen("cmrdns");
            if (RDNS.logHandle == NULL) {
                XplConsolePrintf("cmrdns: Unable to initialize logging.  Logging disabled.\r\n");
            }

            /* Fill out registration information */
            registration->priority  = 1;            /* It is important that the rdns are early */
            registration->Shutdown  = RDNSShutdown;
            registration->Verify    = RDNSVerify;
            registration->Notify    = NULL;

			XplSafeIncrement(RDNS.threadCount);

            strcpy(RDNS.config.datadir, datadir);

            XplOpenLocalSemaphore(RDNS.sqlite.semaphore, 1);

            if (ReadConfiguration()) {
                unsigned char buffer[XPL_MAX_PATH + 1];
                int ccode;
                XplThreadID id;

                snprintf(buffer, sizeof(buffer), "%s/cache.db", datadir);
                if (sqlite3_open(buffer, &RDNS.sqlite.handle) != SQLITE_OK) {
                    printf("cmrdns: Failed to open database: %s\n", sqlite3_errmsg(RDNS.sqlite.handle));
                }

                if (sqlite3_exec(RDNS.sqlite.handle, SQL_CREATE, NULL, NULL, NULL) != SQLITE_OK) {
#if 0
                    printf("bongogatekeeper: %s\n", sqlite3_errmsg(RDNS.sqlite.handle));
#endif
                    sqlite3_exec(RDNS.sqlite.handle, SQL_ROLLBACK, NULL, NULL, NULL);
                }

                if ((sqlite3_prepare(RDNS.sqlite.handle, SQL_ADD,   -1, &RDNS.sqlite.stmt.add,  0) != SQLITE_OK) ||
                    (sqlite3_prepare(RDNS.sqlite.handle, SQL_GET,   -1, &RDNS.sqlite.stmt.get,  0) != SQLITE_OK) ||
                    (sqlite3_prepare(RDNS.sqlite.handle, SQL_CLEAN, -1, &RDNS.sqlite.stmt.clean,0) != SQLITE_OK))
                {
                    printf("cmrdns: Failed to prepare sql: %s\n", sqlite3_errmsg(RDNS.sqlite.handle));
                }

                /* Begin the transaction controller thread */
                XplBeginThread(&id, TransactionController, (1024 * 32), NULL, ccode);

                return(TRUE);
            }
        } else {
            XplConsolePrintf("cmrdns: Failed to obtain directory handle\r\n");
        }
    }

    return(FALSE);
}

EXPORT BOOL 
RDNSShutdown(void)
{
    XplSafeDecrement(RDNS.threadCount);

    if (RDNSUnloadOk == FALSE) {
        RDNSUnloadOk = TRUE;

        /*
            Make sure the library rdns are gone before beginning to 
            shutdown.
        */
        while (XplSafeRead(RDNS.threadCount)) {
            XplDelay(33);
        }

        LoggerClose(RDNS.logHandle);

        sqlite3_finalize(RDNS.sqlite.stmt.add);
        sqlite3_finalize(RDNS.sqlite.stmt.get);
        sqlite3_finalize(RDNS.sqlite.stmt.clean);

        sqlite3_close(RDNS.sqlite.handle);
        XplCloseLocalSemaphore(RDNS.sqlite.semaphore);

#if defined(NETWARE) || defined(LIBC)
        XplSignalLocalSemaphore(RDNS.shutdownSemaphore);	/* The signal will release main() */
        XplWaitOnLocalSemaphore(RDNS.shutdownSemaphore);	/* The wait will wait until main() is gone */

        XplCloseLocalSemaphore(RDNS.shutdownSemaphore);
#endif
    }

    return(FALSE);
}

static BOOL
ReadConfiguration(void)
{
    MDBValueStruct *config;

    config = MDBCreateValueStruct(RDNS.directoryHandle, MsgGetServerDN(NULL));

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMRDNS, MSGSRV_A_DISABLED, config)) {
        if (atol(config->Value[0]) == 1) {
            MDBDestroyValueStruct(config);
            RDNSShutdown();
            return(FALSE);
        }
    }

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMRDNS, MSGSRV_A_CONFIG_CHANGED, config)) {
        RDNS.config.last = atol(config->Value[0]);
        MDBFreeValues(config);
    } else {
        RDNS.config.last = 0;
    }

    /*
        Store the timeout strings.  It is faster to do this once, instead of storing the long value
        and creating the string each time, since it must be a string that is bound to the SQL statement.
    */
    snprintf(RDNS.config.blockTimeout, sizeof(RDNS.config.blockTimeout), "%d minutes", (3 * 60));   /* Default to 3 hours */
    snprintf(RDNS.config.allowTimeout, sizeof(RDNS.config.blockTimeout), "%d minutes", (3 * 60));   /* Default to 3 hours */

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMRDNS, MSGSRV_A_CONFIGURATION, config) > 0) {
        unsigned long i;

        for (i = 0; i < config->Used; i++) {
            if (XplStrNCaseCmp(config->Value[i], BLOCK_TIMEOUT_PREFIX, sizeof(BLOCK_TIMEOUT_PREFIX) - 1) == 0) {
                snprintf(RDNS.config.blockTimeout, sizeof(RDNS.config.blockTimeout), "%lu minutes", atol(config->Value[i] + sizeof(BLOCK_TIMEOUT_PREFIX) - 1));
            } else if (XplStrNCaseCmp(config->Value[i], ALLOW_TIMEOUT_PREFIX, sizeof(ALLOW_TIMEOUT_PREFIX) - 1) == 0) {
                snprintf(RDNS.config.allowTimeout, sizeof(RDNS.config.allowTimeout), "%lu minutes", atol(config->Value[i] + sizeof(ALLOW_TIMEOUT_PREFIX) - 1));
            }
        }
    }

    MDBDestroyValueStruct(config);

    return(TRUE);
}

/*
    Below are "stock" functions that are basically infrastructure.
    However, one might want to add initialization code to main
    and takedown code to the signal handler
*/
void 
RDNSShutdownSigHandler(int Signal)
{
    int	oldtgid = XplSetThreadGroupID(RDNS.tgid);

    if (RDNSUnloadOk == FALSE) {
        RDNSUnloadOk = TRUE;

        /*
            Make sure the library rdns are gone before beginning to 
            shutdown.
        */
        while (XplSafeRead(RDNS.threadCount) > 1) {
            XplDelay(33);
        }

        /* Do any required cleanup */
        LoggerClose(RDNS.logHandle);

#if defined(NETWARE) || defined(LIBC)
        XplSignalLocalSemaphore(RDNS.shutdownSemaphore);  /* The signal will release main() */
        XplWaitOnLocalSemaphore(RDNS.shutdownSemaphore);  /* The wait will wait until main() is gone */

        XplCloseLocalSemaphore(RDNS.shutdownSemaphore);
#endif
    }

    XplSetThreadGroupID(oldtgid);

    return;
}

int 
_NonAppCheckUnload(void)
{
    if (RDNSUnloadOk == FALSE) {
        XplConsolePrintf("\rThis module will automatically be unloaded by the thread that loaded it.\n");
        XplConsolePrintf("\rIt does not allow manual unloading.\n");

        return(1);
    }

    return(0);
}

int main(int argc, char *argv[])
{
    /* init globals */
    RDNS.tgid = XplGetThreadGroupID();

    XplRenameThread(GetThreadID(), "Gate Keeper RDNS Module");
    XplOpenLocalSemaphore(RDNS.shutdownSemaphore, 0);
    XplSignalHandler(RDNSShutdownSigHandler);

    /*
        This will "park" the module 'til we get unloaded; 
        it would not be neccessary to do this on NetWare, 
        but to prevent from automatically exiting on Unix
        we need to keep main around...
    */
    XplWaitOnLocalSemaphore(RDNS.shutdownSemaphore);
    XplSignalLocalSemaphore(RDNS.shutdownSemaphore);

    return(0);
}
