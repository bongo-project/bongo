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

#include "cmuser.h"

/* Globals */
BOOL UserUnloadOk = TRUE;
UserGlobal User;

static BOOL ReadConfiguration(void);
static void Cleanup(void *param);


EXPORT BOOL UserNotify(ConnMgrCommand *command)
{
    /*
        CM_EVENT_AUTHENTICATED
        When receieved, create a file in datadir where the name is the ip address
        and the conntent is the username.
    */

    if (XplStrCaseCmp(command->event, CM_EVENT_AUTHENTICATED) == 0) {
        unsigned char buffer[XPL_MAX_PATH + 1];
        FILE *addr;

        snprintf(buffer, sizeof(buffer), "%s/%07lx", User.config.datadir, command->address);
        addr = fopen(buffer, "w");
        if (addr) {
            fwrite(command->detail.authenticated.user, sizeof(unsigned char), strlen(command->detail.authenticated.user), addr);
            fclose(addr);
        }
    }

    return(TRUE);
}

EXPORT int UserVerify(ConnMgrCommand *command, ConnMgrResult *result)
{
    /*
        CM_EVENT_RELAY
        When received, look for a file in datadir with the ip address as the
        name, and read the user from the content of the file.  Check the time
        of the file to see if it has expired.
    */
    if (XplStrCaseCmp(command->event, CM_EVENT_RELAY) == 0) {
        unsigned char buffer[XPL_MAX_PATH + 1];
        struct stat sb;

        snprintf(buffer, sizeof(buffer), "%s/%07lx", User.config.datadir, command->address);
        if (stat(buffer, &sb) != -1) {
            if (sb.st_mtime > time(NULL) - (User.config.timeout * 60)) { 
                FILE *addr = fopen(buffer, "r");

                if (addr) {
                    int l = fread(result->detail.user, sizeof(unsigned char), MAXEMAILNAMESIZE, addr);
                    result->detail.user[l] = '\0';

                    fclose(addr);
                    return(CM_MODULE_ACCEPT);
                }
            } else {
                unlink(buffer);
            }
        }
        return(CM_MODULE_DENY);
    }

    return(0);
}

EXPORT BOOL
CMUSERInit(CMModuleRegistrationStruct *registration, unsigned char *datadir)
{
    if (UserUnloadOk == TRUE) {
        XplSafeWrite(User.threadCount, 0);

		User.directoryHandle = (MDBHandle) MsgInit();
        if (User.directoryHandle) {
            UserUnloadOk = FALSE;

            User.logHandle = LoggerOpen("cmuser");
            if (User.logHandle == NULL) {
                XplConsolePrintf("cmuser: Unable to initialize logging.  Logging disabled.\r\n");
            }

            /* Fill out registration information */
            registration->priority  = 5;
            registration->Shutdown  = UserShutdown;
            registration->Verify    = UserVerify;
            registration->Notify    = UserNotify;

			XplSafeIncrement(User.threadCount);

            strcpy(User.config.datadir, datadir);

            if (ReadConfiguration()) {
                XplThreadID id;
                int ccode;

                XplBeginThread(&id, Cleanup, (1024 * 32), NULL, ccode);
                return(TRUE);
            }
        } else {
            XplConsolePrintf("cmuser: Failed to obtain directory handle\r\n");
        }
    }

    return(FALSE);
}

EXPORT BOOL 
UserShutdown(void)
{
    XplSafeDecrement(User.threadCount);

    if (UserUnloadOk == FALSE) {
        UserUnloadOk = TRUE;

        /*
            Make sure the library users are gone before beginning to 
            shutdown.
        */
        while (XplSafeRead(User.threadCount)) {
            XplDelay(33);
        }

        LoggerClose(User.logHandle);

#if defined(NETWARE) || defined(LIBC)
        XplSignalLocalSemaphore(User.shutdownSemaphore);	/* The signal will release main() */
        XplWaitOnLocalSemaphore(User.shutdownSemaphore);	/* The wait will wait until main() is gone */

        XplCloseLocalSemaphore(User.shutdownSemaphore);
#endif
    }

    return(FALSE);
}

static BOOL
ReadConfiguration(void)
{
    MDBValueStruct *config;

    config = MDBCreateValueStruct(User.directoryHandle, MsgGetServerDN(NULL));

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMUSER, MSGSRV_A_DISABLED, config)) {
        if (atol(config->Value[0]) == 1) {
            MDBDestroyValueStruct(config);
            UserShutdown();
            return(FALSE);
        }
    }

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMUSER, MSGSRV_A_CONFIG_CHANGED, config)) {
        User.config.last = atol(config->Value[0]);
        MDBFreeValues(config);
    } else {
        User.config.last = 0;
    }

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMUSER, MSGSRV_A_TIMEOUT, config)) {
        User.config.timeout = atol(config->Value[0]);
    } else {
        User.config.timeout = 15;
    }

    MDBDestroyValueStruct(config);

    return(TRUE);
}

static void
Cleanup(void *param)
{
    while (!UserUnloadOk) {
        int i;
        XplDir *dir;
        XplDir *dirent;
        struct stat sb;

        if ((dir = XplOpenDir(User.config.datadir)) != NULL) {
            while ((dirent = XplReadDir(dir)) != NULL) {
                unsigned char buffer[XPL_MAX_PATH +1 ];

                snprintf(buffer, sizeof(buffer), "%s/%s", User.config.datadir, dirent->d_nameDOS);
                if ((stat(buffer, &sb) != -1) && (sb.st_mtime < time(NULL) - (User.config.timeout * 60))) {
                    unlink(buffer);
                }
            }
        }

        for (i = 0; i < (User.config.timeout * 60) && !UserUnloadOk; i++) {
            XplDelay(1000);
        }
    }
}

/*
    Below are "stock" functions that are basically infrastructure.
    However, one might want to add initialization code to main
    and takedown code to the signal handler
*/
void 
UserShutdownSigHandler(int Signal)
{
    int	oldtgid = XplSetThreadGroupID(User.tgid);

    if (UserUnloadOk == FALSE) {
        UserUnloadOk = TRUE;

        /*
            Make sure the library users are gone before beginning to 
            shutdown.
        */
        while (XplSafeRead(User.threadCount) > 1) {
            XplDelay(33);
        }

        /* Do any required cleanup */
        LoggerClose(User.logHandle);

#if defined(NETWARE) || defined(LIBC)
        XplSignalLocalSemaphore(User.shutdownSemaphore);  /* The signal will release main() */
        XplWaitOnLocalSemaphore(User.shutdownSemaphore);  /* The wait will wait until main() is gone */

        XplCloseLocalSemaphore(User.shutdownSemaphore);
#endif
    }

    XplSetThreadGroupID(oldtgid);

    return;
}

int 
_NonAppCheckUnload(void)
{
    if (UserUnloadOk == FALSE) {
        XplConsolePrintf("\rThis module will automatically be unloaded by the thread that loaded it.\n");
        XplConsolePrintf("\rIt does not allow manual unloading.\n");

        return(1);
    }

    return(0);
}

int main(int argc, char *argv[])
{
    /* init globals */
    User.tgid = XplGetThreadGroupID();

    XplRenameThread(GetThreadID(), "Gate Keeper User Module");
    XplOpenLocalSemaphore(User.shutdownSemaphore, 0);
    XplSignalHandler(UserShutdownSigHandler);

    /*
        This will "park" the module 'til we get unloaded; 
        it would not be neccessary to do this on NetWare, 
        but to prevent from automatically exiting on Unix
        we need to keep main around...
    */
    XplWaitOnLocalSemaphore(User.shutdownSemaphore);
    XplSignalLocalSemaphore(User.shutdownSemaphore);

    return(0);
}
