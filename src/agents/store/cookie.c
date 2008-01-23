/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
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
#include <logger.h>
#include <nmlib.h>
#include <msgapi.h>

#include "stored.h"
#include "messages.h"

CCode
StoreCommandCOOKIENEW(StoreClient *client, uint64_t timeout)
{
	MsgAuthCookie cookie;
	NLockStruct *lock = NULL;
	CCode ccode = 0;
    
	assert(STORE_PRINCIPAL_USER == client->principal.type);

	if (NLOCK_ACQUIRED != ReadNLockAcquire(&lock, NULL, client->principal.name, 
                                           STORE_ROOT_GUID, 2000))
		return ConnWriteStr(client->conn, MSG4120USERLOCKED);

	if (NLOCK_ACQUIRED != PurgeNLockAcquire(lock, 2000)) {
		ReadNLockRelease(lock);
		return ConnWriteStr(client->conn, MSG4120USERLOCKED);
	}
	
	if (MsgAuthCreateCookie(client->principal.name, &cookie, timeout)) {
		ccode = ConnWriteF(client->conn, "1000 %.32s\r\n", cookie.token);		
	} else {
                ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
	}
	
	PurgeNLockRelease(lock);
	ReadNLockRelease(lock);
	
	return ccode;
}


CCode
StoreCommandCOOKIEDELETE(StoreClient *client, const char *token)
{
    CCode ccode = 0;
    NLockStruct *lock = NULL;

    assert(STORE_PRINCIPAL_USER == client->principal.type);

    if (NLOCK_ACQUIRED != ReadNLockAcquire(&lock, NULL, client->principal.name, 
                                           STORE_ROOT_GUID, 2000))
    {
        return ConnWriteStr(client->conn, MSG4120USERLOCKED);
    }
    if (NLOCK_ACQUIRED != PurgeNLockAcquire(lock, 2000)) {
        ReadNLockRelease(lock);
        return ConnWriteStr(client->conn, MSG4120USERLOCKED);
    }

    if (MsgAuthDeleteCookie(client->principal.name, token)) {
        ccode = ConnWriteStr(client->conn, MSG1000OK);
    } else {
        ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
    }

    PurgeNLockRelease(lock);
    ReadNLockRelease(lock);

    return ccode;
}

#if 0
// this looks like multiple-store code
CCode
TunnelAuthCookie(StoreClient *client, 
                 struct sockaddr_in *serv, char *user, char *token, int nouser)
{
    Connection *conn = NULL;
    char buffer[1024];
    CCode ccode;

    conn = NMAPConnect(NULL, serv);
    if (!conn) {
        return ConnWriteStr(client->conn, MSG3242BADAUTH);
    }

    ccode = NMAPAuthenticateWithCookie(conn, user, token, buffer, sizeof(buffer));
    switch (ccode) {
    case 1000:
        ccode = SelectUser(client, user, NULL, nouser);
        break;
    case -1:
        break;
    default:
        if (ccode >= 1000 && ccode < 6000) {
            ccode = ConnWriteF(client->conn, "%d %s\r\n", ccode, buffer);
        } else {
            XplConsolePrintf("Unexpected response from remote store: %s\r\n",
                             buffer);
            ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
        }
        break;
    }

    NMAPQuit(conn);
    ConnFree(conn);

    return ccode;
}
#endif

CCode 
StoreCommandAUTHCOOKIE(StoreClient *client, char *user, char *token, int nouser)
{
    CCode ccode;
    NLockStruct *lock = NULL;
//    struct sockaddr_in serv;

    if (StoreAgent.installMode) {
        // don't allow cookie logins in installation mode. FIXME: better error message?
        return ConnWriteStr(client->conn, MSG3242BADAUTH);
    }

    if (0 != MsgAuthFindUser(user)) {
        XplConsolePrintf("Couldn't find user object for %s\r\n", user);
        ccode = ConnWriteStr(client->conn, MSG3242BADAUTH);
        XplDelay(2000);
        goto finish;
    }

    // FIXME: This checks for a non-local store. For Bongo 1.0, we assume a single store.
#if 0
    if (serv.sin_addr.s_addr != MsgGetHostIPAddress()) {
        /* non-local store, need to verify against user's server */
        
        ccode = TunnelAuthCookie(client, &serv, user, token, nouser);
        goto finish;
    }
#endif
    
    if (NLOCK_ACQUIRED != ReadNLockAcquire(&lock, NULL, user, STORE_ROOT_GUID, 2000))
    {
        return ConnWriteStr(client->conn, MSG4120USERLOCKED);
    }
    if (NLOCK_ACQUIRED != PurgeNLockAcquire(lock, 2000)) {
        ReadNLockRelease(lock);
        return ConnWriteStr(client->conn, MSG4120USERLOCKED);
    }

    switch (MsgAuthFindCookie(user, token)) {
    case 0:
        XplDelay(2000);        
        ccode = ConnWriteStr(client->conn, MSG3242BADAUTH);
        break;
    case 1:
        ccode = SelectUser(client, user, NULL, nouser);
        break;
    default:
        ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
        break;
    }

finish:
    if (lock) {
        PurgeNLockRelease(lock);
        ReadNLockRelease(lock);
    }

    return ccode;
}


