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

#include "cmrbl.h"

/* Globals */
BOOL RBLUnloadOk = TRUE;
RBLGlobal RBL;

static BOOL ReadConfiguration(void);


EXPORT int RBLVerify(ConnMgrCommand *command, ConnMgrResult *result)
{
    unsigned long i;

    if (XplStrCaseCmp(command->event, CM_EVENT_CONNECT) == 0) {
        XplDnsRecord *dns = NULL;
        BOOL blocked = FALSE;
        int rc = -1;
        int commentID = -1;

        /* Check for a cached value */
        XplWaitOnLocalSemaphore(RBL.sqlite.semaphore);

        sqlite3_bind_int(RBL.sqlite.stmt.get, 1, command->address);

        if ((rc = sqlite3_step(RBL.sqlite.stmt.get)) == SQLITE_ROW) {
            blocked = sqlite3_column_int(RBL.sqlite.stmt.get, 0);
            strncpy(result->comment, sqlite3_column_text(RBL.sqlite.stmt.get, 1), sizeof(result->comment));
        }

        sqlite3_reset(RBL.sqlite.stmt.get);
        XplSignalLocalSemaphore(RBL.sqlite.semaphore);

        if (rc != SQLITE_ROW) {
            /* Perform the RBL check */
            for (i = 0; i < RBL.rbl.count; i++) {
                unsigned char buffer[MAXEMAILNAMESIZE];
                struct in_addr ip;

                ip.s_addr = command->address;

                /* address is in host byte order.  RBL checks require least significant first.  */
                snprintf(buffer, sizeof(buffer), "%d.%d.%d.%d.%s",
                    ip.s_net,
                    ip.s_host,
                    ip.s_lh,
                    ip.s_impno,
                    RBL.rbl.hosts[i]);

                rc = XplDnsResolve(buffer, &dns, XPL_RR_A);
                if (dns) {
                    MemFree(dns);
                }

                if (rc == XPL_DNS_SUCCESS) {
                    blocked = TRUE;
                    commentID = RBL.rbl.ids[i];
                    strncpy(result->comment, RBL.rbl.comments[i], sizeof(result->comment));
                    continue;
                }
            }

            /* Store new value */
            XplWaitOnLocalSemaphore(RBL.sqlite.semaphore);

            /*
                Insert the new value into the table.  The time for a negative result is a
                full day.  The time for a positive result is only 15 minutes however,
                because a positive result simply means that the servers do not know anything
                about the address.
            */
            sqlite3_bind_int(RBL.sqlite.stmt.add, 1, command->address);
            sqlite3_bind_text(RBL.sqlite.stmt.add, 2, blocked ? RBL.config.blockTimeout : RBL.config.allowTimeout, -1, SQLITE_TRANSIENT);
            sqlite3_bind_int(RBL.sqlite.stmt.add, 3, blocked);
            sqlite3_bind_int(RBL.sqlite.stmt.add, 4, commentID);

            sqlite3_step(RBL.sqlite.stmt.add);

            sqlite3_reset(RBL.sqlite.stmt.add);
            XplSignalLocalSemaphore(RBL.sqlite.semaphore);
        }

        if (blocked) {
            /* He was blocked by RBL */
            return(CM_MODULE_DENY | CM_MODULE_PERMANENT | CM_MODULE_FORCE);
        } else {
            /* He was not blocked, don't force it though */
            return(CM_MODULE_ACCEPT);
        }
    }

    return(0);
}

static void
TransactionController(void *ignored)
{
    XplWaitOnLocalSemaphore(RBL.sqlite.semaphore);
    if (sqlite3_exec(RBL.sqlite.handle, SQL_BEGIN, NULL, NULL, NULL) != SQLITE_OK) {
        XplConsolePrintf("cmrbl: Failed to start transaction controller\n");
    }
    XplSignalLocalSemaphore(RBL.sqlite.semaphore);

    while (!RBLUnloadOk) {
        unsigned long i;

        for (i = 0; i < 15 && !RBLUnloadOk; i++) {
            XplDelay(1000);
        }

        XplWaitOnLocalSemaphore(RBL.sqlite.semaphore);
        if (sqlite3_exec(RBL.sqlite.handle, SQL_SAVE, NULL, NULL, NULL) != SQLITE_OK) {
            XplConsolePrintf("cmrbl: Failed to save cache data\n");
        }
        XplSignalLocalSemaphore(RBL.sqlite.semaphore);
    }

    XplWaitOnLocalSemaphore(RBL.sqlite.semaphore);
    if (sqlite3_exec(RBL.sqlite.handle, SQL_END, NULL, NULL, NULL) != SQLITE_OK) {
        XplConsolePrintf("cmrbl: Failed to end transaction controller\n");
    }
    XplSignalLocalSemaphore(RBL.sqlite.semaphore);
}

EXPORT BOOL
CMRBLInit(CMModuleRegistrationStruct *registration, unsigned char *datadir)
{
    if (RBLUnloadOk == TRUE) {
        XplSafeWrite(RBL.threadCount, 0);

		RBL.directoryHandle = (MDBHandle) MsgInit();
        if (RBL.directoryHandle) {
            RBLUnloadOk = FALSE;

            RBL.logHandle = LoggerOpen("cmrbl");
            if (RBL.logHandle == NULL) {
                XplConsolePrintf("cmrbl: Unable to initialize logging.  Logging disabled.\r\n");
            }

            /* Fill out registration information */
            registration->priority  = 1;            /* It is important that the rbl are early */
            registration->Shutdown  = RBLShutdown;
            registration->Verify    = RBLVerify;
            registration->Notify    = NULL;

			XplSafeIncrement(RBL.threadCount);

            strcpy(RBL.config.datadir, datadir);

            XplOpenLocalSemaphore(RBL.sqlite.semaphore, 1);

            if (ReadConfiguration()) {
                unsigned char buffer[XPL_MAX_PATH + 1];
                int ccode;
                XplThreadID id;
                sqlite3_stmt *stmt;
                sqlite3_stmt *stmt2;
                unsigned long i;

                snprintf(buffer, sizeof(buffer), "%s/cache.db", datadir);
                if (sqlite3_open(buffer, &RBL.sqlite.handle) != SQLITE_OK) {
                    printf("cmrbl: Failed to open database: %s\n", sqlite3_errmsg(RBL.sqlite.handle));
                }

                if (sqlite3_exec(RBL.sqlite.handle, SQL_CREATE, NULL, NULL, NULL) != SQLITE_OK) {
#if 0
                    printf("bongogatekeeper: %s\n", sqlite3_errmsg(RBL.sqlite.handle));
#endif
                    sqlite3_exec(RBL.sqlite.handle, SQL_ROLLBACK, NULL, NULL, NULL);
                }

                if ((sqlite3_prepare(RBL.sqlite.handle, SQL_ADD,   -1, &RBL.sqlite.stmt.add,  0) != SQLITE_OK) ||
                    (sqlite3_prepare(RBL.sqlite.handle, SQL_GET,   -1, &RBL.sqlite.stmt.get,  0) != SQLITE_OK) ||
                    (sqlite3_prepare(RBL.sqlite.handle, SQL_CLEAN, -1, &RBL.sqlite.stmt.clean,0) != SQLITE_OK))
                {
                    printf("cmrbl: Failed to prepare sql: %s\n", sqlite3_errmsg(RBL.sqlite.handle));
                }

                /* Cleanup */
                sqlite3_exec(RBL.sqlite.handle, SQL_COMMENTS_CLEAN1, NULL, NULL, NULL);

                sqlite3_prepare(RBL.sqlite.handle, SQL_COMMENTS_CLEAN2, -1, &stmt, 0);
                for (i = 0; i < RBL.rbl.count; i++) {
                    sqlite3_bind_text(stmt, 1, RBL.rbl.hosts[i], -1, SQLITE_TRANSIENT);
                    sqlite3_step(stmt);
                    sqlite3_reset(stmt);
                }
                sqlite3_finalize(stmt);

                sqlite3_exec(RBL.sqlite.handle, SQL_COMMENTS_CLEAN3, NULL, NULL, NULL);

                /* Ok, add any new ones */
                sqlite3_prepare(RBL.sqlite.handle, SQL_COMMENTS_ADD, -1, &stmt, 0);
                sqlite3_prepare(RBL.sqlite.handle, SQL_COMMENTS_UPDATE, -1, &stmt2, 0);
                for (i = 0; i < RBL.rbl.count; i++) {
                    sqlite3_bind_text(stmt, 1, RBL.rbl.hosts[i], -1, SQLITE_TRANSIENT);
                    sqlite3_bind_text(stmt, 2, RBL.rbl.comments[i], -1, SQLITE_TRANSIENT);

                    if (sqlite3_step(stmt) != SQLITE_DONE)  {
                        /* It is already there, so just update the comment in case it changed */
                        sqlite3_bind_text(stmt2, 1, RBL.rbl.comments[i], -1, SQLITE_TRANSIENT);
                        sqlite3_bind_text(stmt2, 2, RBL.rbl.hosts[i], -1, SQLITE_TRANSIENT);

                        sqlite3_step(stmt2);
                        sqlite3_reset(stmt2);
                    }

                    sqlite3_reset(stmt);
                }
                sqlite3_finalize(stmt);
                sqlite3_finalize(stmt2);

                /* Get the IDs for them */
                sqlite3_prepare(RBL.sqlite.handle, SQL_COMMENTS_GET_ID, -1, &stmt, 0);
                for (i = 0; i < RBL.rbl.count; i++) {
                    sqlite3_bind_text(stmt, 1, RBL.rbl.hosts[i], -1, SQLITE_TRANSIENT);
                    if (sqlite3_step(stmt) == SQLITE_ROW) {
                        RBL.rbl.ids[i] = sqlite3_column_int(RBL.sqlite.stmt.get, 0);
                    } else {
                        RBL.rbl.ids[i] = -1;
                    }
                    sqlite3_reset(stmt);
                }
                sqlite3_finalize(stmt);

                /* Begin the transaction controller thread */
                XplBeginThread(&id, TransactionController, (1024 * 32), NULL, ccode);

                return(TRUE);
            }
        } else {
            XplConsolePrintf("cmrbl: Failed to obtain directory handle\r\n");
        }
    }

    return(FALSE);
}

EXPORT BOOL 
RBLShutdown(void)
{
    XplSafeDecrement(RBL.threadCount);

    if (RBLUnloadOk == FALSE) {
        unsigned long i;

        RBLUnloadOk = TRUE;

        /*
            Make sure the library rbl are gone before beginning to 
            shutdown.
        */
        while (XplSafeRead(RBL.threadCount)) {
            XplDelay(33);
        }

        LoggerClose(RBL.logHandle);

        sqlite3_finalize(RBL.sqlite.stmt.add);
        sqlite3_finalize(RBL.sqlite.stmt.get);
        sqlite3_finalize(RBL.sqlite.stmt.clean);

        sqlite3_close(RBL.sqlite.handle);
        XplCloseLocalSemaphore(RBL.sqlite.semaphore);

        for (i = 0; i < RBL.rbl.count; i++) {
            MemFree(RBL.rbl.hosts[i]);
            MemFree(RBL.rbl.comments[i]);
        }

        if (RBL.rbl.hosts) {
            MemFree(RBL.rbl.hosts);
        }

        if (RBL.rbl.comments) {
            MemFree(RBL.rbl.comments);
        }

        if (RBL.rbl.ids) {
            MemFree(RBL.rbl.ids);
        }

#if defined(NETWARE) || defined(LIBC)
        XplSignalLocalSemaphore(RBL.shutdownSemaphore);	/* The signal will release main() */
        XplWaitOnLocalSemaphore(RBL.shutdownSemaphore);	/* The wait will wait until main() is gone */

        XplCloseLocalSemaphore(RBL.shutdownSemaphore);
#endif
    }

    return(FALSE);
}

static BOOL
ReadConfiguration(void)
{
    MDBValueStruct *config;

    config = MDBCreateValueStruct(RBL.directoryHandle, MsgGetServerDN(NULL));

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMRBL, MSGSRV_A_DISABLED, config)) {
        if (atol(config->Value[0]) == 1) {
            MDBDestroyValueStruct(config);
            RBLShutdown();
            return(FALSE);
        }
    }

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMRBL, MSGSRV_A_CONFIG_CHANGED, config)) {
        RBL.config.last = atol(config->Value[0]);
        MDBFreeValues(config);
    } else {
        RBL.config.last = 0;
    }

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMRBL, MSGSRV_A_RBL_HOST, config) > 0) {
        unsigned long i;

        for (i = 0; i < RBL.rbl.count; i++) {
            MemFree(RBL.rbl.hosts[i]);
            MemFree(RBL.rbl.comments[i]);
        }

        if (RBL.rbl.hosts) {
            MemFree(RBL.rbl.hosts);
        }

        if (RBL.rbl.comments) {
            MemFree(RBL.rbl.comments);
        }

        if (RBL.rbl.ids) {
            MemFree(RBL.rbl.ids);
        }

        RBL.rbl.count = config->Used;
        RBL.rbl.hosts = MemMalloc(config->Used * sizeof(char *));
        RBL.rbl.comments = MemMalloc(config->Used * sizeof(char *));
        RBL.rbl.ids = MemMalloc(config->Used * sizeof(long));

        if (!RBL.rbl.hosts || !RBL.rbl.comments || !RBL.rbl.ids) {
            LoggerEvent(RBL.logHandle, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, config->Used * sizeof(char *) * 2, __LINE__, NULL, 0);
        } else {
            for (i = 0; i < RBL.rbl.count; i++) {
                unsigned char *ptr = strchr(config->Value[i], ';');

                if (ptr) {
                    ptr[0] = '\0';
                }

                RBL.rbl.hosts[i] = MemStrdup(config->Value[i]);
                if (ptr) {
                    RBL.rbl.comments[i] = MemStrdup(ptr + 1);
                } else {
                    RBL.rbl.comments[i] = MemMalloc(sizeof(BLOCKED_STRING) + strlen(RBL.rbl.hosts[i]));
                    sprintf(RBL.rbl.comments[i], BLOCKED_STRING, RBL.rbl.hosts[i]);
                }

                LoggerEvent(RBL.logHandle, LOGGER_SUBSYSTEM_CONFIGURATION, LOGGER_EVENT_CONFIGURATION_STRING, LOG_INFO, 0, "MSGSRV_A_RBL_HOST", config->Value[i], 0, 0, NULL, 0);
            }
        }

        MDBFreeValues(config);
    }

    /*
        Store the timeout strings.  It is faster to do this once, instead of storing the long value
        and creating the string each time, since it must be a string that is bound to the SQL statement.
    */
    snprintf(RBL.config.blockTimeout, sizeof(RBL.config.blockTimeout), "%d minutes", (24 * 60));    /* Default to 1 day for a block     */
    snprintf(RBL.config.allowTimeout, sizeof(RBL.config.blockTimeout), "%d minutes", 15);           /* Default to 15 minutes for allow  */

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMRBL, MSGSRV_A_CONFIGURATION, config) > 0) {
        unsigned long i;

        for (i = 0; i < config->Used; i++) {
            if (XplStrNCaseCmp(config->Value[i], BLOCK_TIMEOUT_PREFIX, sizeof(BLOCK_TIMEOUT_PREFIX) - 1) == 0) {
                snprintf(RBL.config.blockTimeout, sizeof(RBL.config.blockTimeout), "%lu minutes", atol(config->Value[i] + sizeof(BLOCK_TIMEOUT_PREFIX) - 1));
            } else if (XplStrNCaseCmp(config->Value[i], ALLOW_TIMEOUT_PREFIX, sizeof(ALLOW_TIMEOUT_PREFIX) - 1) == 0) {
                snprintf(RBL.config.allowTimeout, sizeof(RBL.config.allowTimeout), "%lu minutes", atol(config->Value[i] + sizeof(ALLOW_TIMEOUT_PREFIX) - 1));
            }
        }
    }

    MDBDestroyValueStruct(config);

    if (RBL.rbl.count == 0) {
#if VERBOSE
        XplConsolePrintf("cmrbl: No RBL zones configured.  RBL check is disabled.\n");
#endif
        RBLShutdown();
        return(FALSE);
    }

    return(TRUE);
}

/*
    Below are "stock" functions that are basically infrastructure.
    However, one might want to add initialization code to main
    and takedown code to the signal handler
*/
void 
RBLShutdownSigHandler(int Signal)
{
    int	oldtgid = XplSetThreadGroupID(RBL.tgid);

    if (RBLUnloadOk == FALSE) {
        RBLUnloadOk = TRUE;

        /*
            Make sure the library rbl are gone before beginning to 
            shutdown.
        */
        while (XplSafeRead(RBL.threadCount) > 1) {
            XplDelay(33);
        }

        /* Do any required cleanup */
        LoggerClose(RBL.logHandle);

#if defined(NETWARE) || defined(LIBC)
        XplSignalLocalSemaphore(RBL.shutdownSemaphore);  /* The signal will release main() */
        XplWaitOnLocalSemaphore(RBL.shutdownSemaphore);  /* The wait will wait until main() is gone */

        XplCloseLocalSemaphore(RBL.shutdownSemaphore);
#endif
    }

    XplSetThreadGroupID(oldtgid);

    return;
}

int 
_NonAppCheckUnload(void)
{
    if (RBLUnloadOk == FALSE) {
        XplConsolePrintf("\rThis module will automatically be unloaded by the thread that loaded it.\n");
        XplConsolePrintf("\rIt does not allow manual unloading.\n");

        return(1);
    }

    return(0);
}

int main(int argc, char *argv[])
{
    /* init globals */
    RBL.tgid = XplGetThreadGroupID();

    XplRenameThread(GetThreadID(), "Gate Keeper RBL Module");
    XplOpenLocalSemaphore(RBL.shutdownSemaphore, 0);
    XplSignalHandler(RBLShutdownSigHandler);

    /*
        This will "park" the module 'til we get unloaded; 
        it would not be neccessary to do this on NetWare, 
        but to prevent from automatically exiting on Unix
        we need to keep main around...
    */
    XplWaitOnLocalSemaphore(RBL.shutdownSemaphore);
    XplSignalLocalSemaphore(RBL.shutdownSemaphore);

    return(0);
}
