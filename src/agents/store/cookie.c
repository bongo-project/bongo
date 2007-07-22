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

#include "stored.h"
#include "messages.h"

typedef struct {
    char token[32];
    uint64_t expirationDate;
} Cookie;


static void
GetCookiesPath(StoreClient *client, const char *user, char *path, size_t len)
{
    const char *root;
    int n;

    n = snprintf(path, len, "%s/%s/cookies", StoreAgent.store.rootDir, user);
}


static int
AddCookie(StoreClient *client, Cookie *cookie)
{
    char path[XPL_MAX_PATH+1];
    FILE *f;
    long len;

    GetCookiesPath(client, client->principal.name, path, sizeof(path));

    f = fopen(path, "a");
    if (!f) {
        return -1;
    }

    len = ftell(f);

    if (sizeof(Cookie) != fwrite(cookie, 1, sizeof(Cookie), f) ||
        fclose(f))
    {
        fclose(f);
        truncate(path, len);
        return -1;
    }

    return 0;
}


/* returns: 1 if cookie found, 0 if not found, -1 on error */

static int
FindCookie(StoreClient *client, const char *user, const char *token)
{
    char path[XPL_MAX_PATH+1];
    FILE *f;
    Cookie cookie;
    int result = 0;
    uint64_t now;
    int expired = 0;

    now = BongoCalTimeAsUint64(BongoCalTimeNow(NULL));

    GetCookiesPath(client, user, path, sizeof(path));
    
    f = fopen(path, "r");
    if (!f) {
        return 0;
    }

    while (sizeof(Cookie) == fread(&cookie, 1, sizeof(Cookie), f)) {
        if (cookie.expirationDate < now) {
            ++expired;
            continue;
        }
        if (!strncmp(cookie.token, token, sizeof(cookie.token))) {
            result = 1;
            break;
        }
    }

    if (ferror(f)) {
        result = -1;
    }

    if (expired > 8) {
        /* FIXME: purge expired cookies from the file */
    }

    (void) fclose(f);
        
    return result;
}


CCode
StoreCommandCOOKIENEW(StoreClient *client, uint64_t timeout)
{
	Cookie cookie;
	NLockStruct *lock = NULL;
	CCode ccode = 0;
    	unsigned char credential[XPLHASH_MD5_LENGTH];
	xpl_hash_context context;

	cookie.expirationDate = timeout;
	
	XplHashNew(&context, XPLHASH_MD5);
	
	snprintf(cookie.token, sizeof(cookie.token), "%x%x", 
			(unsigned int) XplGetThreadID(), 
			(unsigned int) time(NULL));
	XplHashWrite(&context, cookie.token, strlen(cookie.token));
	XplRandomData(cookie.token, 8);
	XplHashWrite(&context, cookie.token, strlen(cookie.token));
	XplHashFinal(&context, XPLHASH_LOWERCASE, cookie.token, XPLHASH_MD5_LENGTH);
    
	assert(STORE_PRINCIPAL_USER == client->principal.type);

	if (NLOCK_ACQUIRED != ReadNLockAcquire(&lock, NULL, client->principal.name, 
                                           STORE_ROOT_GUID, 2000))
		return ConnWriteStr(client->conn, MSG4120USERLOCKED);

	if (NLOCK_ACQUIRED != PurgeNLockAcquire(lock, 2000)) {
		ReadNLockRelease(lock);
		return ConnWriteStr(client->conn, MSG4120USERLOCKED);
	}
	
	if (AddCookie(client, &cookie)) {
		ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
	} else {
		ccode = ConnWriteF(client->conn, "1000 %.32s\r\n", cookie.token);
	}
	
	PurgeNLockRelease(lock);
	ReadNLockRelease(lock);
	
	return ccode;
}


CCode
StoreCommandCOOKIEDELETE(StoreClient *client, const char *token)
{
    CCode ccode = 0;
    char path[XPL_MAX_PATH+1];
    FILE *f;
    NLockStruct *lock = NULL;
    Cookie cookie;

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

    GetCookiesPath(client, client->principal.name, path, sizeof(path));

    if (!token) {
        if (unlink(path)) {
            printf("Couldn't unlink cookie file %s\r\n", path);
            ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
        } else {
            ccode = ConnWriteStr(client->conn, MSG1000OK);
        }
        goto finish;
    }

    f = fopen(path, "r+");
    if (!f) {
        ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
        goto finish;
    }

    while (sizeof(Cookie) == fread(&cookie, 1, sizeof(Cookie), f)) {
        if (!strncmp(cookie.token, token, sizeof(cookie.token))) {
            /* FIXME: should (atomically) re-generate the cookie file instead of 
               overwriting the delete cookie with zeroes */
            memset(&cookie, 0, sizeof(Cookie));
            if (fseek(f, -sizeof(Cookie), SEEK_CUR) || 
                sizeof(Cookie) != fwrite(&cookie, 1, sizeof(Cookie), f))
            {
                ccode = ConnWriteStr(client->conn, MSG5004INTERNALERR);
                goto finish;
            }
        }   
    }
           
    if (ferror(f)) {
        goto finish;
    }
    
    ccode = ConnWriteStr(client->conn, MSG1000OK);

finish:
    PurgeNLockRelease(lock);
    ReadNLockRelease(lock);

    return ccode;
}


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

    if (! MsgAuthFindUser(user)) {
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

    switch (FindCookie(client, user, token)) {
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


