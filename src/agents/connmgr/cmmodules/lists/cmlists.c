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

#include "cmlists.h"

/* Globals */
BOOL ListUnloadOk = TRUE;
ListGlobal List;

static BOOL ReadConfiguration(void);


EXPORT int ListsVerify(ConnMgrCommand *command, ConnMgrResult *result)
{
    unsigned long i;

    /* Check Allowed List */
    if ((XplStrCaseCmp(command->event, CM_EVENT_RELAY) == 0) || (XplStrCaseCmp(command->event, CM_EVENT_CONNECT) == 0)) {
        for (i = 0; i < List.allowed.count; i++) {
            if ((List.allowed.start[i] <= (long) command->address) && ((long) command->address <= List.allowed.end[i])) {
                /* We like the guy */
                return(CM_MODULE_ACCEPT | CM_MODULE_PERMANENT | CM_MODULE_FORCE);
            }
        }
    }

    /* Check Blocked List */
    if (XplStrCaseCmp(command->event, CM_EVENT_CONNECT) == 0) {
        for (i = 0; i < List.blocked.count; i++) {
            if ((List.blocked.start[i] <= (long) command->address) && ((long) command->address <= List.blocked.end[i])) {
                /* We don't like the guy */
                return(CM_MODULE_DENY | CM_MODULE_PERMANENT | CM_MODULE_FORCE);
            }
        }
    }

    return(0);
}

EXPORT BOOL
CMLISTSInit(CMModuleRegistrationStruct *registration, unsigned char *datadir)
{
    if (ListUnloadOk == TRUE) {
        XplSafeWrite(List.threadCount, 0);

		List.directoryHandle = (MDBHandle) MsgInit();
        if (List.directoryHandle) {
            ListUnloadOk = FALSE;

            List.logHandle = LoggerOpen("cmlist");
            if (List.logHandle == NULL) {
                XplConsolePrintf("cmlist: Unable to initialize logging.  Logging disabled.\r\n");
            }

            /* Fill out registration information */
            registration->priority  = 1;            /* It is important that the lists are early */
            registration->Shutdown  = ListsShutdown;
            registration->Verify    = ListsVerify;
            registration->Notify    = NULL;

			XplSafeIncrement(List.threadCount);

            strcpy(List.config.datadir, datadir);

            return(ReadConfiguration());
        } else {
            XplConsolePrintf("cmlist: Failed to obtain directory handle\r\n");
        }
    }

    return(FALSE);
}

static void
AddBlockedAddress(const unsigned char *AddressStart, const unsigned char *AddressEnd)
{
	if ((List.blocked.count + 1) > List.blocked.allocated) {
		List.blocked.start = MemRealloc(List.blocked.start, (List.blocked.allocated + 5) * sizeof(unsigned long));
		List.blocked.end = MemRealloc(List.blocked.end, (List.blocked.allocated + 5) * sizeof(unsigned long));

		if (!List.blocked.start || !List.blocked.end) {
			LoggerEvent(List.logHandle, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, ((List.blocked.allocated+5) + (List.blocked.allocated+5)) * sizeof(unsigned long), __LINE__, NULL, 0);
			return;
		}

		List.blocked.allocated += 5;
	}

	if ((List.blocked.start[List.blocked.count] = inet_addr(AddressStart)) != -1) {
		List.blocked.start[List.blocked.count] = ntohl(List.blocked.start[List.blocked.count]);

		if ((List.blocked.end[List.blocked.count] = inet_addr(AddressEnd)) != -1) {
			List.blocked.end[List.blocked.count] = ntohl(List.blocked.end[List.blocked.count]);
			List.blocked.count++;
		}
	}
}

static void
FreeBlockedAddresses(void)
{
	if (List.blocked.start) {
		MemFree(List.blocked.start);
	}

	List.blocked.start = NULL;

	if (List.blocked.end) {
		MemFree(List.blocked.end);
	}
	List.blocked.end = NULL;

    List.blocked.allocated = 0;
    List.blocked.count = 0;
}

static void
AddAllowedAddress(const unsigned char *AddressStart, const unsigned char *AddressEnd)
{
	if ((List.allowed.count + 1) > List.allowed.allocated) {
		List.allowed.start = MemRealloc(List.allowed.start, (List.allowed.allocated + 5) * sizeof(unsigned long));
		List.allowed.end = MemRealloc(List.allowed.end, (List.allowed.allocated + 5) * sizeof(unsigned long));

		if (!List.allowed.start || !List.allowed.end) {
			LoggerEvent(List.logHandle, LOGGER_SUBSYSTEM_GENERAL, LOGGER_EVENT_OUT_OF_MEMORY, LOG_ERROR, 0, __FILE__, NULL, ((List.allowed.allocated+5) + (List.allowed.allocated+5)) * sizeof(unsigned long), __LINE__, NULL, 0);
			return;
		}

		List.allowed.allocated += 5;
	}

	if ((List.allowed.start[List.allowed.count] = inet_addr(AddressStart)) != -1) {
		List.allowed.start[List.allowed.count] = ntohl(List.allowed.start[List.allowed.count]);

		if ((List.allowed.end[List.allowed.count] = inet_addr(AddressEnd)) != -1) {
			List.allowed.end[List.allowed.count] = ntohl(List.allowed.end[List.allowed.count]);
			List.allowed.count++;
		}
	}
}

static void
FreeAllowedAddresses(void)
{
	if (List.allowed.start) {
		MemFree(List.allowed.start);
	}

	List.allowed.start = NULL;

	if (List.allowed.end) {
		MemFree(List.allowed.end);
	}

	List.allowed.end = NULL;

    List.allowed.allocated = 0;
    List.allowed.count = 0;
}

EXPORT BOOL 
ListsShutdown(void)
{
    XplSafeDecrement(List.threadCount);

    if (ListUnloadOk == FALSE) {
        ListUnloadOk = TRUE;

        /*
            Make sure the library lists are gone before beginning to 
            shutdown.
        */
        while (XplSafeRead(List.threadCount)) {
            XplDelay(33);
        }

        LoggerClose(List.logHandle);
        FreeBlockedAddresses();
        FreeAllowedAddresses();

#if defined(NETWARE) || defined(LIBC)
        XplSignalLocalSemaphore(List.shutdownSemaphore);	/* The signal will release main() */
        XplWaitOnLocalSemaphore(List.shutdownSemaphore);	/* The wait will wait until main() is gone */

        XplCloseLocalSemaphore(List.shutdownSemaphore);
#endif
    }

    return(FALSE);
}

static BOOL
ReadConfiguration(void)
{
    MDBValueStruct *config;
	const unsigned char	*Address;
	MDBEnumStruct *e;
    unsigned char *ptr;

    config = MDBCreateValueStruct(List.directoryHandle, MsgGetServerDN(NULL));
	e = MDBCreateEnumStruct(config);

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMLISTS, MSGSRV_A_DISABLED, config)) {
        if (atol(config->Value[0]) == 1) {
            MDBDestroyValueStruct(config);
            ListsShutdown();
            return(FALSE);
        }
    }

    if (MDBRead(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMLISTS, MSGSRV_A_CONFIG_CHANGED, config)) {
        List.config.last = atol(config->Value[0]);
        MDBFreeValues(config);
    } else {
        List.config.last = 0;
    }

    while ((Address = MDBReadEx(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMLISTS, MSGSRV_A_BLOCKED_ADDRESS, e, config)) != NULL) {
        ptr = strchr(Address, '-');
        if (ptr) {
            *ptr = '\0';
            AddBlockedAddress(Address, ptr + 1);
            *ptr = '-';
        } else {
            AddBlockedAddress(Address, Address);
        }
    }
    MDBFreeValues(config);
    
    while ((Address = MDBReadEx(MSGSRV_AGENT_CONNMGR "\\" MSGSRV_AGENT_CMLISTS, MSGSRV_A_ALLOWED_ADDRESS, e, config)) != NULL) {
        ptr = strchr(Address, '-');
        if (ptr) {
            *ptr = '\0';
            AddAllowedAddress(Address, ptr + 1);
            *ptr = '-';
        } else {
            AddAllowedAddress(Address, Address);
        }
    }
    MDBFreeValues(config);

    MDBDestroyEnumStruct(e, config);
    MDBDestroyValueStruct(config);

    if (List.blocked.count == 0 && List.allowed.count == 0) {
#if VERBOSE
        XplConsolePrintf("cmlists: No addresses blocked or allowed\n");
#endif
    
        ListsShutdown();
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
ListShutdownSigHandler(int Signal)
{
    int	oldtgid = XplSetThreadGroupID(List.tgid);

    if (ListUnloadOk == FALSE) {
        ListUnloadOk = TRUE;

        /*
            Make sure the library lists are gone before beginning to 
            shutdown.
        */
        while (XplSafeRead(List.threadCount) > 1) {
            XplDelay(33);
        }

        /* Do any required cleanup */
        LoggerClose(List.logHandle);

#if defined(NETWARE) || defined(LIBC)
        XplSignalLocalSemaphore(List.shutdownSemaphore);  /* The signal will release main() */
        XplWaitOnLocalSemaphore(List.shutdownSemaphore);  /* The wait will wait until main() is gone */

        XplCloseLocalSemaphore(List.shutdownSemaphore);
#endif
    }

    XplSetThreadGroupID(oldtgid);

    return;
}

int 
_NonAppCheckUnload(void)
{
    if (ListUnloadOk == FALSE) {
        XplConsolePrintf("\rThis module will automatically be unloaded by the thread that loaded it.\n");
        XplConsolePrintf("\rIt does not allow manual unloading.\n");

        return(1);
    }

    return(0);
}

int main(int argc, char *argv[])
{
    /* init globals */
    List.tgid = XplGetThreadGroupID();

    XplRenameThread(GetThreadID(), "Gate Keeper List Module");
    XplOpenLocalSemaphore(List.shutdownSemaphore, 0);
    XplSignalHandler(ListShutdownSigHandler);

    /*
        This will "park" the module 'til we get unloaded; 
        it would not be neccessary to do this on NetWare, 
        but to prevent from automatically exiting on Unix
        we need to keep main around...
    */
    XplWaitOnLocalSemaphore(List.shutdownSemaphore);
    XplSignalLocalSemaphore(List.shutdownSemaphore);

    return(0);
}
