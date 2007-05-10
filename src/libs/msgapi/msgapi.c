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

/* Product defines */

#define PRODUCT_NAME                        "Bongo API Library"
#define PRODUCT_VERSION                     "$Revision: 1.6 $"
#define PRODUCT_SHORT_NAME                  "msgapi.nlm"

#define CURRENT_PRODUCT_VERSION             3
#define CURRENT_PRODUCT_VERSION_MAJOR       5
#define CURRENT_PRODUCT_VERSION_MINOR       3

#include <config.h>
#include <xpl.h>
#include <xplresolve.h>
#include <connio.h>

#include <nmap.h>
#include <nmlib.h>
#include <msgapi.h>
#include "msgapip.h"

#include <bongo-buildinfo.h>
#include <bongoutil.h>
#include <bongostore.h>
#include <curl/curl.h>

#define MSGAPI_FLAG_EXITING                 (1 << 0)
#define MSGAPI_FLAG_STANDALONE              (1 << 1)
#define MSGAPI_FLAG_SUPPORT_CONNMGR         (1 << 2)
#define MSGAPI_FLAG_CLUSTERED               (1 << 3)
#define MSGAPI_FLAG_BOUND                   (1 << 4)
#define MSGAPI_FLAG_INTERNET_EMAIL_ADDRESS  (1 << 5)
#define MSGAPI_FLAG_MONITOR_RUNNING         (1 << 6)

#define LOOPBACK_ADDRESS                    0x0100007F
#define EMPTY_CHAR                          0x01

#define CONNMGR_STRUCT_SIZE                 (sizeof(ConnectionManagerCommand) + MAXEMAILNAMESIZE)

#define CONNMGR_REPLY_NOT_FOUND             0
#define CONNMGR_REPLY_FOUND                 1
#define CONNMGR_REPLY_FOUND_NAME            2

#define CONNMGR_REQ_STORE                   0
#define CONNMGR_REQ_CHECK                   1
#define CONNMGR_REQ_STORE_NAME              2
#define CONNMGR_REQ_CHECK_NAME              3

/* I made sure this is defined in xplschema.h --Ryan 
   We should remove it from here at some point. */
#if !defined(A_INTERNET_EMAIL_ADDRESS)
#define A_INTERNET_EMAIL_ADDRESS            "Internet EMail Address"
#endif

enum LibraryStates {
    LIBRARY_LOADED = 0, 
    LIBRARY_INITIALIZING, 
    LIBRARY_RUNNING, 
    LIBRARY_SHUTTING_DOWN, 
    LIBRARY_SHUTDOWN, 

    LIBRARY_MAX_STATES
} MSGAPIState = LIBRARY_LOADED;

struct {
    unsigned long flags;

    MDBHandle directoryHandle;

    XplThreadID groupID;

    struct {
        XplSemaphore version;
        XplSemaphore shutdown;
        XplMutex uid;
    } sem;

    struct {
        unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
    } server;

    struct {
        MDBValueStruct *storePaths;
        MDBValueStruct *storeDNs;

        struct {
            MDBValueStruct *names;
            MDBValueStruct *contexts;
            struct sockaddr_in *addr;
        } server;

#ifdef PARENTOBJECT_STORE
        struct {
            MDBValueStruct *objects;
            MDBValueStruct *stores;
            MDBValueSTruct *indices;
        } parent;
#endif
    } vs;

    unsigned long connManager;

    XplRWLock configLock;

    struct {
        unsigned char work[XPL_MAX_PATH + 1];
        unsigned char nls[XPL_MAX_PATH + 1];
        unsigned char dbf[XPL_MAX_PATH + 1];
        unsigned char bin[XPL_MAX_PATH + 1];
        unsigned char lib[XPL_MAX_PATH + 1];
        unsigned char certificate[XPL_MAX_PATH + 1];
        unsigned char key[XPL_MAX_PATH + 1];
    } paths;

    struct {
        unsigned long local;

        unsigned char string[16];
    } address;

    struct {
        unsigned char moduleName[XPL_MAX_PATH + 1];
        unsigned char fileName[XPL_MAX_PATH + 1];

        XplPluginHandle handle;

        FindObjectCacheInitType init;
        FindObjectCacheShutdownType shutdown;
        FindObjectCacheType find;
        FindObjectCacheExType findEx;
        FindObjectStoreCacheType store;
    } cache;

    struct {
        unsigned char domain[MAXEMAILNAMESIZE + 1];
        unsigned long domainLength;
    } official;

    XplAtomic useCount;
} MsgGlobal;

typedef struct _ConnectionManagerCommand {
    unsigned long request;
    unsigned long address;
    unsigned long nameLen;
    unsigned long counter;
    unsigned char name[1];
} ConnectionManagerCommand;

/* I tried to remove this, but it's still needed :(
 * Python server for Dragonfly needs a way to authenticate itself
 * to the queue...
 */
void 	 
MsgNmapChallenge(const unsigned char *response, unsigned char *reply, size_t length) 	 
{ 	 
    unsigned char *ptr; 	 
    unsigned char *salt; 	 
    static unsigned char access[NMAP_HASH_SIZE] = { '\0' }; 	 
    MDBValueStruct *v; 	 
    xpl_hash_context ctx; 	 
 	 
    if (access[0] == '\0') { 	 
        if (MsgGlobal.directoryHandle) { 	 
            v = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL); 	 
            if (v) { 	 
                MDBRead(MSGSRV_ROOT, MSGSRV_A_ACL, v); 	 
                if (v->Used) { 	 
                    HashCredential(MsgGlobal.server.dn, v->Value[0], access); 	 
                } 	 
 	 
                MDBDestroyValueStruct(v); 	 
            } 	 
        } 	 
    } 	 
 	 
    if (access[0] && reply && (length > 32) && ((ptr = strchr(response, '<')) != NULL)) { 	 
        salt = ++ptr; 	 
 	 
        if ((ptr = strchr(ptr, '>')) != NULL) { 	 
            *ptr = '\0'; 	 
        } 	 
 	 
        XplHashNew(&ctx, XPLHASH_MD5); 	 
        XplHashWrite(&ctx, salt, strlen(salt)); 	 
        XplHashWrite(&ctx, access, NMAP_HASH_SIZE); 	 
        XplHashFinal(&ctx, XPLHASH_LOWERCASE, reply, XPLHASH_MD5_LENGTH); 	 
    } else if (reply) { 	 
        *reply = '\0'; 	 
    } 	 
 	 
    return; 	 
}

EXPORT const unsigned char *
MsgGetServerDN(unsigned char *buffer)
{
    if (buffer) {
        return(strcpy(buffer, MsgGlobal.server.dn));
    }
    return(MsgGlobal.server.dn);
}

EXPORT BOOL
MsgFindUserNmap(const unsigned char *user, unsigned char *nmap, int nmap_len, unsigned short *port)
{
    struct sockaddr_in nmap_tmp;

    BOOL retVal = FALSE;
    retVal = MsgFindObject(user, NULL, NULL, &nmap_tmp, NULL);

    if (retVal) {
        strncpy(nmap, inet_ntoa(nmap_tmp.sin_addr), nmap_len);
        *port = ntohs(nmap_tmp.sin_port);
        return retVal;
    }

    return retVal;
}

EXPORT BOOL
MsgFindObject(const unsigned char *user, unsigned char *dn, unsigned char *userType, struct sockaddr_in *nmap, MDBValueStruct *v)
{
    BOOL retVal = FALSE;

    if (MsgGlobal.cache.find == NULL) {
        MDBValueStruct *userCtx;
        MDBValueStruct *types;
        unsigned long i, j;
        unsigned char rdn[MDB_MAX_OBJECT_CHARS + 1];

        BOOL isUser = FALSE;
        BOOL isGroup = FALSE;
        BOOL isResource = FALSE;
        BOOL isOrgRole = FALSE;
        BOOL isDynGroup = FALSE;

        /* FIXME: handle failure to create ValueStructs */
        userCtx = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);
        types = MDBShareContext(userCtx);

        XplRWReadLockAcquire(&MsgGlobal.configLock);
        for (i = 0; i < MsgGlobal.vs.server.contexts->Used; i++) {
            MDBSetValueStructContext(MsgGlobal.vs.server.contexts->Value[i], userCtx);
            if (MDBGetObjectDetailsEx(user, types, rdn, dn, userCtx)) {

                /* determine relevant types now so we don't have to loop
                   throught the list everytime. */
                for (j = 0; j < types->Used; j++) {
                    if (strcmp(types->Value[j], C_USER) == 0) {
                        isUser = TRUE;
                    } else if (strcmp(types->Value[j], 
                                      MSGSRV_C_RESOURCE) == 0) {
                        isResource = TRUE;
                    } else if (strcmp(types->Value[j], MSGSRV_C_GROUP) == 0 ||
                               strcmp(types->Value[j], C_GROUP) == 0) {
                        isGroup = TRUE;
                    } else if (strcmp(types->Value[j], 
                                      MSGSRV_C_ORGANIZATIONAL_ROLE) == 0 ||
                               strcmp(types->Value[j], 
                                      C_ORGANIZATIONAL_ROLE) == 0) {
                        isOrgRole = TRUE;
                    } else if (strcmp(types->Value[j], "dynamicGroup") == 0) {
                        isDynGroup = TRUE;
                    }
                }

                if (MsgGetUserSetting(user, MSGSRV_A_MESSAGING_DISABLED, 
                                      userCtx) > 0) {
                    if (userCtx->Value[userCtx->Used-1][0] == '1') {
                        break;
                    } else {
                        MDBFreeValue(userCtx->Used - 1, userCtx);
                    }
                }

                if (isUser || isResource) {
                    if (v) {
                        MDBAddValue(rdn, v);
                    }
                    if (nmap) {
                        memcpy(nmap, &MsgGlobal.vs.server.addr[i], sizeof(struct sockaddr_in));
                    }
                    if (userType) {
                        if (isUser) {
                            strcpy(userType, C_USER);
                        } else if (isResource) {
                            strcpy(userType, MSGSRV_C_RESOURCE);
                        }
                    }
                    retVal = TRUE;
                } else if (isGroup) {
                    if (v) {
                        MDBSetValueStructContext(MsgGlobal.vs.server.contexts->Value[i], v);
                        MDBRead(user, A_MEMBER, v);

                        /* This removes any context from a group member's name */
                        for (j = 0; j < v->Used; j++) {
                            unsigned char *ptr;
                            ptr = strrchr(v->Value[j], '\\');
                            if (ptr) {
                                memmove(v->Value[j], ptr + 1, strlen(ptr + 1) + 1);
                            }
                        }
                    }
                    if (userType) {
                        strcpy(userType, C_GROUP);
                    }
                    retVal = TRUE;
                } else if (isOrgRole) {
                    if (v) {
                        MDBSetValueStructContext(MsgGlobal.vs.server.contexts->Value[i], v);
                        MDBRead(user, A_ROLE_OCCUPANT, v);

                        /* This removes any context from a the roles name */
                        for (j = 0; j < v->Used; j++) {
                            unsigned char *ptr;
                            ptr = strrchr(v->Value[j], '\\');
                            if (ptr) {
                                memmove(v->Value[j], ptr + 1, strlen(ptr + 1) + 1);
                            }
                        }
                    }
                    if (userType) {
                        strcpy(userType, C_ORGANIZATIONAL_ROLE);
                    }
                    retVal = TRUE;
                } else if (isDynGroup) {
                    if (v) {
                        MDBSetValueStructContext(MsgGlobal.vs.server.contexts->Value[i], v);
                        MDBRead(user, A_MEMBER, v);

                        /* This removes any context from a group member's name */
                        for (j = 0; j < v->Used; j++) {
                            unsigned char *ptr;
                            ptr=strrchr(v->Value[j], '\\');
                            if (ptr) {
                                memmove(v->Value[j], ptr + 1, strlen(ptr + 1) + 1);
                            }
                        }
                    }
                    if (userType) {
                        strcpy(userType, "dynamicGroup");
                    }
                    retVal = TRUE;
                }
                break;
            }
        }
        XplRWReadLockRelease(&MsgGlobal.configLock);

        MDBDestroyValueStruct(userCtx);
        MDBDestroyValueStruct(types);
    } else {
        retVal = MsgGlobal.cache.find(user, dn, userType, nmap, v);
    }

    return(retVal);
}

EXPORT BOOL
MsgFindObjectEx(const unsigned char *user, unsigned char *dn, unsigned char *userType, struct sockaddr_in *nmap, BOOL *disabled, MDBValueStruct *v)
{
    BOOL retVal = FALSE;

    if (MsgGlobal.cache.findEx == NULL) {
        MDBValueStruct *userCtx;
        MDBValueStruct *types;
        unsigned long i, j;
        unsigned char rdn[MDB_MAX_OBJECT_CHARS + 1];

        BOOL isUser = FALSE;
        BOOL isGroup = FALSE;
        BOOL isResource = FALSE;
        BOOL isOrgRole = FALSE;
        BOOL isDynGroup = FALSE;

        userCtx = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);
        types = MDBShareContext(userCtx);

        XplRWReadLockAcquire(&MsgGlobal.configLock);

        for (i = 0; i < MsgGlobal.vs.server.contexts->Used; i++) {
            MDBSetValueStructContext(MsgGlobal.vs.server.contexts->Value[i], userCtx);

            if (MDBGetObjectDetailsEx(user, types, rdn, dn, userCtx)) {
                for (j = 0; j < types->Used; j++) {
                    if (strcmp(types->Value[j], C_USER) == 0) {
                        isUser = TRUE;
                    } else if (strcmp(types->Value[j], 
                                      MSGSRV_C_RESOURCE) == 0) {
                        isResource = TRUE;
                    } else if (strcmp(types->Value[j], MSGSRV_C_GROUP) == 0 ||
                               strcmp(types->Value[j], C_GROUP) == 0) {
                        isGroup = TRUE;
                    } else if (strcmp(types->Value[j], 
                                      MSGSRV_C_ORGANIZATIONAL_ROLE) == 0 ||
                               strcmp(types->Value[j], 
                                      C_ORGANIZATIONAL_ROLE) == 0) {
                        isOrgRole = TRUE;
                    } else if (strcmp(types->Value[j], "dynamicGroup") == 0) {
                        isDynGroup = TRUE;
                    }
                }

                if (MsgGetUserSetting(user, MSGSRV_A_MESSAGING_DISABLED, 
                                      userCtx) > 0) {
                    if (userCtx->Value[userCtx->Used - 1][0] == '1') {
                        *disabled = TRUE;
                    } else {
                        *disabled = FALSE;
                    }
                    MDBFreeValue(userCtx->Used - 1, userCtx);
                } else {
                    *disabled = FALSE;
                }

                if (isUser || isResource) {
                    if (v) {
                        MDBAddValue(rdn, v);
                    }
                    if (nmap) {
                        memcpy(nmap, &MsgGlobal.vs.server.addr[i], sizeof(struct sockaddr_in));
                    }
                    if (userType) {
                        if (isUser) {
                            strcpy(userType, C_USER);
                        } else if (isResource) {
                            strcpy(userType, MSGSRV_C_RESOURCE);
                        }
                    }
                    retVal = TRUE;
                } else if (isGroup) {
                    if (v) {
                        MDBSetValueStructContext(MsgGlobal.vs.server.contexts->Value[i], v);
                        MDBRead(user, A_MEMBER, v);

                        /* This removes any context from a group member's name */
                        for (j = 0; j < v->Used; j++) {
                            unsigned char *ptr;
                            ptr = strrchr(v->Value[j], '\\');
                            if (ptr) {
                                memmove(v->Value[j], ptr + 1, strlen(ptr + 1) + 1);
                            }
                        }
                    }
                    if (userType) {
                        strcpy(userType, C_GROUP);
                    }
                    retVal = TRUE;
                } else if (isOrgRole) {
                    if (v) {
                        MDBSetValueStructContext(MsgGlobal.vs.server.contexts->Value[i], v);
                        MDBRead(user, A_ROLE_OCCUPANT, v);

                        /* This removes any context from a the roles name */
                        for (j = 0; j < v->Used; j++) {
                            unsigned char *ptr;
                            ptr = strrchr(v->Value[j], '\\');
                            if (ptr) {
                                memmove(v->Value[j], ptr + 1, strlen(ptr + 1) + 1);
                            }
                        }
                    }
                    if (userType) {
                        strcpy(userType, C_ORGANIZATIONAL_ROLE);
                    }
                    retVal = TRUE;
                } else if (isDynGroup) {
                    if (v) {
                        MDBSetValueStructContext(MsgGlobal.vs.server.contexts->Value[i], v);
                        MDBRead(user, A_MEMBER, v);

                        /* This removes any context from a group member's name */
                        for (j = 0; j < v->Used; j++) {
                            unsigned char *ptr;
                            ptr = strrchr(v->Value[j], '\\');
                            if (ptr) {
                                memmove(v->Value[j], ptr + 1, strlen(ptr + 1) + 1);
                            }
                        }
                    }
                    if (userType) {
                        strcpy(userType, "dynamicGroup");
                    }
                    retVal = TRUE;
                }
                break;
            }
        }
        XplRWReadLockRelease(&MsgGlobal.configLock);

        MDBDestroyValueStruct(userCtx);
    } else {
        retVal = MsgGlobal.cache.findEx(user, dn, userType, nmap, disabled, v);
    }

    return(retVal);
}

EXPORT const unsigned char *
MsgFindUserStore(const unsigned char *user, const unsigned char *defaultPath)
{
    if (MsgGlobal.cache.store == NULL) {
        MDBValueStruct *context;
        unsigned long i;
        
        XplRWReadLockAcquire(&MsgGlobal.configLock);
        context = MDBCreateValueStruct(MsgGlobal.directoryHandle, MsgGlobal.vs.server.contexts->Value[0]);

        i = 0;
        do {
            if (MDBIsObject(user, context)) {
                MDBDestroyValueStruct(context);
                if (MsgGlobal.vs.storePaths->Value[i][0] != EMPTY_CHAR) {
                    XplRWReadLockRelease(&MsgGlobal.configLock);
                    return(MsgGlobal.vs.storePaths->Value[i]);
                } else {
                    XplRWReadLockRelease(&MsgGlobal.configLock);
                    return(defaultPath);
                }
            } else {
                i++;
                if (i != MsgGlobal.vs.server.contexts->Used) {
                    MDBSetValueStructContext(MsgGlobal.vs.server.contexts->Value[i], context);
                }
            }
        } while (i != MsgGlobal.vs.server.contexts->Used);
        XplRWReadLockRelease(&MsgGlobal.configLock);

        MDBDestroyValueStruct(context);
        return(defaultPath);
    }

    return MsgGlobal.cache.store(user, defaultPath);
}

EXPORT BOOL
MsgReadIP(const unsigned char *object, unsigned char *type, MDBValueStruct *v)
{
    int first,last, i;
    unsigned char *ptr;

    if (!v || !type) {
        return(FALSE);
    }

    first=v->Used;

    if (!object) {
        if (!MDBReadDN(MsgGlobal.server.dn, type, v)) {
            return(FALSE);
        }
    } else {
        if (!MDBReadDN(object, type, v)) {
            return(FALSE);
        }
    }

    last = v->Used;

    for (i = first; i < last; i++) {
        if ((ptr = strrchr(v->Value[i], '\\')) != NULL) {
            *ptr = '\0';
        }
        MDBRead(v->Value[i], MSGSRV_A_IP_ADDRESS, v);
    }

    /* The following loop removes the names read by the ReadDN above,
     * but keeps the IP addresses added afterwards :-) */
    for (i = first; i < last; i++) {
        MDBFreeValue(0, v);
    }

    if (v->Used > 0) {
        return(TRUE);
    } else {
        return(FALSE);
    }
}

EXPORT BOOL
MsgDomainExists(const unsigned char *domain, unsigned char *domainObjectDN)
{
    MDBValueStruct *domains;
    unsigned long i;
    unsigned long j;

    /* FIXME later; when we handle parent objects */
    return(FALSE);

    domains = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);

    XplRWReadLockAcquire(&MsgGlobal.configLock);

    for (i = 0; i < MsgGlobal.vs.server.names->Used; i++) {
        MDBSetValueStructContext(MsgGlobal.vs.server.names->Value[i], domains);
        if(MDBRead(MSGSRV_AGENT_SMTP, MSGSRV_A_DOMAIN, domains) > 0) {
            for (j = 0; j < domains->Used; j++) {
                if (XplStrCaseCmp(domain, domains->Value[j])==0) {
                    XplRWReadLockRelease(&MsgGlobal.configLock);
                    MDBDestroyValueStruct(domains);

                    return(TRUE);
                }
            }
        }
        MDBFreeValues(domains);
    }
    MDBDestroyValueStruct(domains);
    XplRWReadLockRelease(&MsgGlobal.configLock);

    return(FALSE);
}

EXPORT int
MsgGetParentAttribute(const unsigned char *userDN, unsigned char *attribute, MDBValueStruct *vOut)
{
    char configDn[MDB_MAX_OBJECT_CHARS + 1];
    unsigned long index;
    MDBValueStruct *v;
    int result = 0;

    configDn[0] = '\0';

    if (MsgGetUserSettingsDN(userDN, configDn, vOut, FALSE)) {
        if (MDBRead(configDn, MSGSRV_A_PARENT_OBJECT, vOut) > 0) {
            index = vOut->Used - 1;
            if (MDBRead(v->Value[index], attribute, vOut)>0) {
                MDBFreeValue(index, vOut);
                result = vOut->Used;
            } else {
                MDBFreeValue(index, vOut);
                result = MDBRead(userDN, attribute, vOut);
            }
        } else {
            result = MDBRead(userDN, attribute, vOut);
        }
    }

    return result;
}

EXPORT unsigned char *
MsgGetUserEmailAddress(const unsigned char *userDNin, MDBValueStruct *userData, unsigned char *buffer, unsigned long bufLen)
{
    unsigned char *emailAddress = NULL;
    unsigned long originalValueCount = userData->Used;
    unsigned long lastValueCount = originalValueCount;
    unsigned char *user = NULL;
    unsigned char userDN[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char configDn[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char *delim;

    /* Make a copy so we don't edit the const string */ 
    strcpy(userDN, userDNin);

    if (!(MsgGlobal.flags & MSGAPI_FLAG_INTERNET_EMAIL_ADDRESS)) {
        MDBRead(userDN, A_INTERNET_EMAIL_ADDRESS, userData);
        if (userData->Used > lastValueCount) {

            if (strchr(userData->Value[userData->Used - 1], '@')) {
		/* The user object has a value in the Internet Email Address Attribute */
                if (buffer) {
                    if (bufLen > strlen(userData->Value[userData->Used - 1])) {
                        emailAddress = buffer;
                        strcpy(emailAddress, userData->Value[userData->Used - 1]);
                    } 
                } else {
                    emailAddress = MemStrdup(userData->Value[userData->Used - 1]);
                }

                while (originalValueCount < userData->Used){
                    MDBFreeValue(userData->Used - 1, userData);
                }
                return(emailAddress);
            } else {
                user = userData->Value[userData->Used - 1];
                lastValueCount++;
            }
        }
    }

    /* The value in the Internet Email Address Attribute still needs a domain */

    delim = strrchr(userDN, '\\' );
    if (delim) {
        if (strchr(delim + 1, '@')) {
            /* The username is an address */
            if (buffer) {
                if (bufLen > strlen(delim + 1)) {
                    emailAddress = buffer;
                    strcpy(emailAddress, delim + 1);
                }                                          
            } else {
                emailAddress = MemStrdup(delim + 1);
            }
	    
            while (originalValueCount < userData->Used) {
		MDBFreeValue(userData->Used - 1, userData);
	    }
	
            return(emailAddress);
        }

        if (!user) {
            user = delim + 1;
        }            


        if (MsgGetParentAttribute(userDN, MSGSRV_A_DOMAIN, userData)) {
            /* The user or parent has a domain defined */
            if (buffer) {
                if (bufLen > (strlen(user) + strlen(userData->Value[userData->Used - 1]) + 1)) {
                    emailAddress = buffer;
                    sprintf(emailAddress, "%s@%s", user, userData->Value[userData->Used - 1]);
                }
            } else {
                emailAddress = MemMalloc(strlen(user) + strlen(userData->Value[userData->Used - 1]) + 2);
                if (emailAddress) {
                    sprintf(emailAddress, "%s@%s", user, userData->Value[userData->Used - 1]);
                }
            }

            while (originalValueCount < userData->Used){
                MDBFreeValue(userData->Used - 1, userData);
            }

            return(emailAddress);
        }

        *delim = '\0';
        if (MsgGetUserSettingsDN(userDN, configDn, userData, FALSE)) {
            MDBRead(configDn, MSGSRV_A_DOMAIN, userData);
        }

        if (userData->Used > lastValueCount) {
            /* The container has a domain defined */
            if (buffer) {
                if (bufLen > (strlen(user) + strlen(userData->Value[userData->Used - 1]) + 1)) {
                    emailAddress = buffer;
                    sprintf(emailAddress, "%s@%s", user, userData->Value[userData->Used - 1]);                
                }
            } else {
                emailAddress = MemMalloc(strlen(user) + strlen(userData->Value[userData->Used - 1]) + 2);
                if (emailAddress) {
                    sprintf(emailAddress, "%s@%s", user, userData->Value[userData->Used - 1]);
                }
            }
        } else {
            /* Default  user@official */ 
            if (buffer) {
                if (bufLen > (strlen(user) + MsgGlobal.official.domainLength + 1)) {
                    emailAddress = buffer;
                    sprintf(emailAddress, "%s@%s", user, MsgGlobal.official.domain);
                }
            } else {
                emailAddress = MemMalloc(strlen(user) + MsgGlobal.official.domainLength + 2);
                if (emailAddress) {
                    sprintf(emailAddress, "%s@%s", user, MsgGlobal.official.domain);
                }
            }
        }

        *delim = '\\';

        for (;originalValueCount < userData->Used;){
            MDBFreeValue(userData->Used - 1, userData);
        }
        return(emailAddress);
    }

    /* The dn does not appear valid.  Treat it as rdn and do our best */
    if (buffer) {
        if (bufLen > (strlen(userDN) + MsgGlobal.official.domainLength + 1)) {
            emailAddress = buffer;
            sprintf(emailAddress, "%s@%s", userDN, MsgGlobal.official.domain);
        }
    } else {
        emailAddress = MemMalloc(strlen(userDN) + MsgGlobal.official.domainLength + 2);
        if (emailAddress) {
            sprintf(emailAddress, "%s@%s", userDN, MsgGlobal.official.domain);
        }
    }

    while (originalValueCount < userData->Used) {
	MDBFreeValue(userData->Used - 1, userData);
    }
    return(emailAddress);
}

EXPORT unsigned char *
MsgGetUserDisplayName(const unsigned char *userDN, MDBValueStruct *userData)
{
    unsigned char *displayName = NULL;
    unsigned long index = userData->Used;
    unsigned char *rdn;
    
    /* Read the Display Name */
    if ((MDBRead(userDN, A_FULL_NAME, userData) > 0) && (userData->Value[index][0] != '\0')) {
        displayName = MemStrdup(userData->Value[index]);
        while (index < userData->Used){
            MDBFreeValue(index, userData);
        }
        return(displayName);
    } 

    /* Create full name using First and Last */
    MDBRead(userDN, A_GIVEN_NAME, userData);
    MDBRead(userDN, A_SURNAME, userData);
    if (userData->Used == (index + 2)) {
        if ((userData->Value[index][0] != '\0') && (userData->Value[index + 1][0] != '\0')) {
	    displayName = MemMalloc (strlen (userData->Value[index]) + strlen (userData->Value[index + 1]) + 2);
            sprintf(displayName, "%s %s", userData->Value[index], userData->Value[index + 1]);
        } else if (userData->Value[index][0] != '\0') {
            displayName = MemStrdup(userData->Value[index]);
        } else if (userData->Value[index + 1][0] != '\0') {
            displayName = MemStrdup(userData->Value[index + 1]);
        } else {
            if ((rdn = strrchr(userDN, '\\'))) {
                displayName = MemStrdup(rdn + 1);
            } else {

                displayName = MemStrdup("");
            }
        }
    } else if (userData->Used == (index + 1)) {
        if (!(userData->Value[index][0] == ' ' && userData->Value[index][1] == '\0')) {
            displayName = MemStrdup(userData->Value[index]);
        } else {
	    /* FIXME: is this right? */
            if ((rdn = strrchr (userDN, '\\'))) {
                displayName = MemStrdup(rdn + 1);
            } else {
                displayName = MemStrdup("");
            }
        }
    } else {
        if ((rdn = strrchr(userDN, '\\'))) {
            displayName = MemStrdup(rdn + 1);
        } else {  
            displayName = MemStrdup("");
        }

    }

    while (index < userData->Used) {
        MDBFreeValue(index, userData);
    }


    return(displayName);
    
}

/* Get the container DN where a given user's settings object should exist. 
   This function is responsible for sorting BongoUserSettings objects into 
   various partitions under the root BongoUserSettingsContainer. */
EXPORT BOOL
MsgGetUserSettingsContainerDN(const unsigned char *userDn, 
                              unsigned char *containerDn, MDBValueStruct *v, 
                              BOOL create)
{
    MDBValueStruct *attr, *data;
    char servicesDn[MDB_MAX_OBJECT_CHARS + 1];
    char dn[MDB_MAX_OBJECT_CHARS + 1];
    BOOL result = FALSE;
    char *bucket;

    /* Use the first letter of the user's name to sort them into containers.
       This is not the most optimal solution, but it should allow admins to 
       find corresponding config objects more easily. */

    if (MsgGetConfigProperty(servicesDn, MSGSRV_CONFIG_PROP_BONGO_SERVICES)) {
        bucket = strrchr(userDn, '\\');

        if (bucket) {
            bucket++;
        } else {
            bucket = userDn;
        }

        sprintf(dn, "%s\\%s\\%c", servicesDn, MSGSRV_USER_SETTINGS_ROOT, 
                *bucket);

        if (MDBIsObject(dn, v)) {
            result = TRUE;
        } else if (create) {
            attr = MDBShareContext(v);
            data = MDBShareContext(v);

            if (attr && data) {
                result = MDBCreateObject(dn, MSGSRV_C_USER_SETTINGS_CONTAINER, 
                                         attr, data, v);
                MDBDestroyValueStruct(attr);
                MDBDestroyValueStruct(data);
            }
        }
    }

    if (result) {
        strcpy(containerDn, dn);
    }

    return result;
}

/*
 * User settings are stored on a corresponding BongoUserSettings object that
 * can be discovered by following the SeeAlso attribute on the User.
 * This function is for convenience in discovering the config object DN.
 */
EXPORT BOOL
MsgGetUserSettingsDN(const unsigned char *userDn, unsigned char *configDn, 
                     MDBValueStruct *v, BOOL create)
{
    int i;
    char type[MDB_MAX_ATTRIBUTE_VALUE_CHARS + 1];
    char containerDn[MDB_MAX_OBJECT_CHARS + 1];
    char *cn;
    MDBValueStruct *vTmp, *attr, *data;
    BOOL result = FALSE;

    vTmp = MDBShareContext(v);

    if (vTmp) {
        if (MDBReadDN(userDn, A_SEE_ALSO, vTmp)) {
            for (i = 0; i < vTmp->Used; i++) {
                if (!MDBGetObjectDetails(vTmp->Value[i], type, NULL, NULL, 
                                         vTmp)) {
                    break;
                }

                if (!strcasecmp(type, MSGSRV_C_USER_SETTINGS)) {
                    strcpy(configDn, vTmp->Value[i]);
                    result = TRUE;
                    break;
                }
            }
        }

        if (!result && create) {
            cn = strrchr(userDn, '\\');
            attr = MDBShareContext(vTmp);
            data = MDBShareContext(vTmp);

            if (cn && attr && data && 
                MsgGetUserSettingsContainerDN(userDn, containerDn, vTmp, 
                                              TRUE)) {
                sprintf(configDn, "%s\\%s", containerDn, ++cn);

                if (MDBCreateObject(configDn, MSGSRV_C_USER_SETTINGS, 
                                    attr, data, vTmp) &&
                    MDBAddDN(userDn, A_SEE_ALSO, configDn, vTmp)) {
                    result = TRUE;
                } else {
                    configDn[0] = '\0';
                }

                MDBDestroyValueStruct(attr);
                MDBDestroyValueStruct(data);
            }
        }

        MDBDestroyValueStruct(vTmp);
    }

    return result;
}

/*
 * Try to get the setting first from the user object, then from the user's 
 * settings object.  Parent object may override values on settings object.
 */
EXPORT long
MsgGetUserSetting(const unsigned char *userDn, unsigned char *attribute, 
                  MDBValueStruct *vOut)
{
    char configDn[MDB_MAX_OBJECT_CHARS + 1];
    char parentDn[MDB_MAX_OBJECT_CHARS + 1];
    MDBValueStruct *v;
    long result = 0;

    configDn[0] = '\0';
    parentDn[0] = '\0';
    v = MDBShareContext(vOut);

    if (v) {
        if (MsgGetUserSettingsDN(userDn, configDn, v, FALSE)) {
            if (MDBReadDN(configDn, MSGSRV_A_PARENT_OBJECT, v)) {
                strcpy(parentDn, v->Value[0]);
                MDBFreeValues(v);

                if (MDBRead(parentDn, MSGSRV_A_FEATURE_INHERITANCE, v)) {
                    if (v->Value[0][0] != FEATURE_PARENT_FIRST) {
                        /* child takes precedence */
                        parentDn[0] = '\0';
                    }
                }
            }
        }

       
        /* try parent object */
        if (*parentDn) {
            result = MDBRead(parentDn, attribute, vOut);
        }

        /* try user object */
        if (!result) {
            result = MDBRead(userDn, attribute, vOut);
        }

        /* try settings object */
        if (!result && *configDn) {
            result = MDBRead(configDn, attribute, vOut);
        }
        MDBDestroyValueStruct(v);
    }

    return result;
}

/*
 * Use these functions to ensure that the correct MDB function is used
 * for strings or DNs.
 */
static BOOL
MsgMdbWriteAny(const unsigned char *dn, const unsigned char *attribute,
               MDBValueStruct *v)
{
    if (v->Used > 0 && strchr(v->Value[0], '\\')) {
        return MDBWriteDN(dn, attribute, v);
    }

    return MDBWrite(dn, attribute, v);
}

static BOOL
MsgMdbAddAny(const unsigned char *dn, const unsigned char *attribute,
             const unsigned char *value, MDBValueStruct *v)
{
    if (v->Used > 0 && strchr(v->Value[0], '\\')) {
        return MDBAddDN(dn, attribute, value, v);
    }

    return MDBAdd(dn, attribute, value, v);
}

/*
 * Try to apply changes to the user object first then the config object.  If 
 * the config object doesn't exist, create it.
 */
EXPORT BOOL
MsgSetUserSetting(const unsigned char *userDn, unsigned char *attribute, 
                  MDBValueStruct *v)
{
    char configDn[MDB_MAX_OBJECT_CHARS + 1];
    BOOL result = FALSE;

    if (MDBIsObject(userDn, v) && 
        !(result = MsgMdbWriteAny(userDn, attribute, v))) {
        if (MsgGetUserSettingsDN(userDn, configDn, v, TRUE)) {
            /* config objects are created on demand */
            result = MsgMdbWriteAny(configDn, attribute, v);
        }
    }

    return result;
}

EXPORT BOOL
MsgAddUserSetting(const unsigned char *userDn, unsigned char *attribute, 
                  unsigned char *value, MDBValueStruct *v)
{
    char configDn[MDB_MAX_OBJECT_CHARS + 1];
    BOOL result = FALSE;

    if (MDBIsObject(userDn, v) && 
        !(result = MsgMdbAddAny(userDn, attribute, value, v))) {
        /* config objects are created on demand */
        if (!MsgGetUserSettingsDN(userDn, configDn, v, TRUE)) {
            result = MsgMdbAddAny(configDn, attribute, value, v);
        }
    }

    return result;
}

/*
 * Here's the logic:
 *
 * Step 1: We get the DN from the caller; we check if there's a parent DN, 
 * Step 2: If we have inheritance DN, so we read the inheritance configuration of the parent, if not, go to step 4c
 * Step 3a: Inheritance is Parent->User; read features attribute from parent
 * Step 3b: Inheritance is User->Parent; read features attribute from user
 * Step 4a: If feature disabled return disabled state
 * Step 4b: If feature from parent, read parent data attribute
 * Step 4c: If feature from user, read user data attribute
 * Step 5: If data attribute empty, try opposite DN attribute
 *
 */
EXPORT BOOL
MsgGetUserFeature(const unsigned char *userDN, unsigned char featureRow, unsigned long featureCol, unsigned char *attribute, MDBValueStruct *vOut)
{
    MDBValueStruct *v;
    unsigned char inheritance = '\0';
    unsigned char parentDN[MDB_MAX_OBJECT_CHARS+1];
    unsigned char configDN[MDB_MAX_OBJECT_CHARS+1];
    const unsigned char *objectDN;
    unsigned long i;
    BOOL looped;
    
    v = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);

    configDN[0] = '\0';
    MsgGetUserSettingsDN(userDN, configDN, v, FALSE);

    /* Step 1 */
    if (configDN && MDBRead(configDN, MSGSRV_A_PARENT_OBJECT, v) > 0) {
	/* We have a parent */

        /* Step 2 */
        strcpy(parentDN, v->Value[0]);

        if (MDBRead(v->Value[0], MSGSRV_A_FEATURE_INHERITANCE, v) > 0) {
            /* We have a configured inheritance */
            if ((inheritance = v->Value[1][0]) == FEATURE_PARENT_FIRST) {            
                objectDN = parentDN;
            } else {
                objectDN = configDN;
            }
        } else {
	    /* No inheritance configured */
            objectDN = configDN;
        }
        MDBFreeValues(v);
    } else {
        parentDN[0] = '\0';
        objectDN = configDN;
    }

    /* objectDN now contains the DN we have determined we need to read first */

    looped=FALSE;

    /* Step 3 */
 ReadFeatureAttribute:
    if (MDBRead(objectDN, MSGSRV_A_FEATURE_SET, v) > 0) {
        /* We have features defined on the object */
        for (i = 0; i < v->Used; i++) {
	    /* Find our feature */
            if (v->Value[i][0] == featureRow) {
                switch (v->Value[i][featureCol]) {
		case FEATURE_NOT_AVAILABLE: {                                        
                    /* Feature disabled; return */
		    MDBDestroyValueStruct(v);

		    return(FALSE);
		}

		case FEATURE_AVAILABLE: {
                    /* Feature enabled */
		    i = v->Used;
		    break;
		}

		case FEATURE_USE_PARENT: 
                    /* Feature enabled via parent */
		case FEATURE_USE_USER: {
		    /* Feature enabled via user */
		    if (!looped && (parentDN[0] != '\0')) {
			if (objectDN == parentDN) {
			    objectDN = configDN;
			} else {
			    objectDN = parentDN;
			}

			MDBFreeValues(v);
			looped=TRUE;
			goto ReadFeatureAttribute;
		    } else { 
                        /* If loop exists, use user object */
			objectDN = configDN;
			i = v->Used;
		    }
		    break;
		}
                }
            }
        }

        /* We now have the DN of the object we need to read the data attribute from in ObjectDN */
        MDBFreeValues(v);
    } else {
        /* We didn't find a feature set configuration */
        if (inheritance == FEATURE_PARENT_FIRST) {
            /* We read the parent, didn't have feature config, let's read the user's feature set */
            inheritance = FEATURE_USER_FIRST;
            objectDN = configDN;
            goto ReadFeatureAttribute;
        } else {
            /* We read the user, don't have inheritance and don't have a feature set -> return enabled feature!    */
            goto ReadDataAttribute;
        }
    }

 ReadDataAttribute:
    /* Beyond this point, we can return success; we returned failure above */

    /* Clean up */
    MDBDestroyValueStruct(v);

    /* If Attribute NULL we just check if feature disabled */
    if (!attribute) {
        return(TRUE);
    }

    return MsgGetUserSetting(configDN, attribute, vOut);
}

EXPORT BOOL
MsgCleanPath(unsigned char *path)
{
    unsigned char *ptr;
    unsigned char *ptr2;

    /* This code strips off any server names that might be configured in the Path */
    ptr = path;
    while((ptr = strchr(ptr, '\\')) != NULL) {
        *ptr = '/';
    }

    ptr=strchr(path, ':');
    if (ptr) {
        *ptr = '\0';
        ptr2 = strchr(path, '/');
        if (ptr2) {
            *ptr=':';
            memmove(path, ptr2+1, strlen(ptr2+1)+1);
        } else {
            *ptr=':';
        }
    }

/* If you want all pathnames lowercase, uncomment the following */
#if 0
    ptr = path;
    while (*ptr) {
        if (isupper(*ptr)) {
            *ptr = tolower(*ptr);
        }
        ptr++;
    }
#endif

    /* strip off trailing /, to allow other code reliable appending of files by using %s/%s */
    ptr = path + strlen(path) - 1;
    if (*ptr == '/') {
        *ptr = '\0';
    }

    return(TRUE);
}

EXPORT void
MsgMakePath(unsigned char *path)
{
    unsigned char *ptr = path;
    unsigned char *ptr2;
    struct stat sb;

    ptr = strchr(path, '/');
    if (!ptr)
        ptr=strchr(path, '\\');

    while (ptr) {
        *ptr = '\0';
        if (stat(path, &sb) != 0) {
            XplMakeDir(path);
        }

        *ptr = '/';
        ptr2 = ptr;
        ptr = strchr(ptr2 + 1, '/');
        if (!ptr)
	    ptr = strchr(ptr2 + 1, '\\');
    }

    if (stat(path, &sb) != 0) {
        XplMakeDir(path);
    }
}

EXPORT const unsigned char *
MsgGetDBFDir(char *directory)
{
    if (directory) {
        strcpy(directory, MsgGlobal.paths.dbf);
    }
    return(MsgGlobal.paths.dbf);
}

EXPORT const unsigned char *
MsgGetWorkDir(char *directory)
{
    if (directory) {
        strcpy(directory, MsgGlobal.paths.work);
    }
    return(MsgGlobal.paths.work);
}

EXPORT const unsigned char *
MsgGetNLSDir(char *directory)
{
    if (directory) {
        strcpy(directory, MsgGlobal.paths.nls);
    }
    return(MsgGlobal.paths.nls);
}

EXPORT const unsigned char *
MsgGetBinDir(char *directory)
{
    if (directory) {
        strcpy(directory, MsgGlobal.paths.bin);
    }
    return(MsgGlobal.paths.bin);
}

EXPORT const unsigned char *
MsgGetLibDir(char *directory)
{
    if (directory) {
        strcpy(directory, MsgGlobal.paths.lib);
    }
    return(MsgGlobal.paths.bin);
}

EXPORT const unsigned char *
MsgGetTLSCertPath(char *path)
{
    if (path) {
        strcpy(path, MsgGlobal.paths.certificate);
    }
    return(MsgGlobal.paths.certificate);
}

EXPORT const unsigned char *
MsgGetTLSKeyPath(char *path)
{
    if (path) {
        strcpy(path, MsgGlobal.paths.key);
    }
    return(MsgGlobal.paths.key);
}

EXPORT unsigned long
MsgGetAgentBindIPAddress(void)
{
    if ((MsgGlobal.flags & MSGAPI_FLAG_BOUND)) {
        return(MsgGlobal.address.local);
    } else {
        return(0);
    }
}

EXPORT unsigned long
MsgGetHostIPAddress(void)
{
    return(MsgGlobal.address.local);
}

EXPORT const char *
MsgGetUnprivilegedUser(void)
{
    if (BONGO_USER[0] == '\0') {
	return NULL;
    } else {
	return BONGO_USER;
    }
}

static BOOL
LoadContextList(MDBValueStruct *config)
{
   unsigned char emptyString[] = " ";
    unsigned char storeObjectDn[MDB_MAX_OBJECT_CHARS + 1];    
    MDBValueStruct *ports;
    MDBValueStruct *addresses;
    char storePortText[6];
    unsigned long i;
    unsigned long j;
    unsigned long count;
    unsigned long realUsed;
 
    ports = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);
    if (!ports) {
        return(FALSE);
    }

    addresses = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);
    if (!addresses) {
        MDBDestroyValueStruct(ports);
        return(FALSE);
    }

    MDBFreeValues(MsgGlobal.vs.server.names);
    MDBFreeValues(MsgGlobal.vs.server.contexts);
    MDBFreeValues(MsgGlobal.vs.storePaths);
    MDBFreeValues(MsgGlobal.vs.storeDNs);
    if (MsgGlobal.vs.server.addr) {
        MemFree(MsgGlobal.vs.server.addr);
        MsgGlobal.vs.server.addr = NULL;
    }

    /* This puts the local context always first */
    sprintf(storeObjectDn, "%s\\%s", MsgGlobal.server.dn, MSGSRV_AGENT_STORE);
    if(MDBIsObject(storeObjectDn, MsgGlobal.vs.server.contexts)) {
        MDBReadDN(MsgGlobal.server.dn, MSGSRV_A_CONTEXT, MsgGlobal.vs.server.contexts);
        if (MDBRead(storeObjectDn, MSGSRV_A_PORT, config)) {
            strcpy(storePortText, config->Value[0]);
            MDBFreeValues(config);
        } else {
            sprintf(storePortText, "%d", (BONGO_STORE_DEFAULT_PORT));
        }
        for (i = 0; i < MsgGlobal.vs.server.contexts->Used; i++) {
            MDBRead(MsgGlobal.server.dn, MSGSRV_A_IP_ADDRESS, addresses);
            MDBAddValue(storePortText, ports);
            MDBAddValue(MsgGlobal.server.dn, MsgGlobal.vs.storeDNs);
        }
    }

#ifdef PARENTOBJECT_STORE
    /* Read the parent object store configuration */
    if (MDBEnumerateObjects(MSGSRV_ROOT"\\"MSGSRV_PARENT_ROOT, MSGSRV_C_PARENTOBJECT, &MsgGlobal.vs.parent.objects)) {
        for (i=0; i<MsgGlobal.vs.parent.objects.Used; i++) {
            if (MDBRead(MsgGlobal.vs.parent.objects.Value[i], MSGSRV_A_MESSAGE_STORE, &MsgGlobal.vs.parent.stores)==0) {
                MDBAddValue(EmptyString, &MsgGlobal.vs.parent.stores);
            }

        }
    }
#endif

    /* FIXME: This code turns off distributed automatically if the server is not in internet services */
    /* We might not always want this? */
#if 0
    if (strstr(storeObjectDn, MSGSRV_ROOT)==NULL) {
        MsgGlobal.flags |= MSGAPI_FLAG_STANDALONE;
    }
#endif

    if (!(MsgGlobal.flags & MSGAPI_FLAG_STANDALONE)) {
        /* First time around we check all "real" classes */
        if (MDBEnumerateObjects(MSGSRV_ROOT, MSGSRV_C_SERVER, NULL, MsgGlobal.vs.server.names)) {
            /* Put the local context(s) first */
            for (i = 0; i < MsgGlobal.vs.server.names->Used; i++) {
                /* MsgGlobal.server.dn is absolute, MsgGlobal.vs.server.names->Value isn't */
                if (XplStrCaseCmp(MsgGlobal.server.dn + strlen(MsgGlobal.server.dn) - strlen(MsgGlobal.vs.server.names->Value[i]), MsgGlobal.vs.server.names->Value[i]) != 0) {
                    count = MsgGlobal.vs.server.contexts->Used;
                    if ((MDBRead(MsgGlobal.vs.server.names->Value[i], MSGSRV_A_IP_ADDRESS, addresses) > 0) &&
                        (MDBReadDN(MsgGlobal.vs.server.names->Value[i], MSGSRV_A_CONTEXT, MsgGlobal.vs.server.contexts) > 0)) {
                        sprintf(storeObjectDn, "%s\\%s", MsgGlobal.vs.server.names->Value[i], MSGSRV_AGENT_STORE);
                        if (MDBRead(storeObjectDn, MSGSRV_A_PORT, config)) {
                            strcpy(storePortText, config->Value[0]);
                            MDBFreeValues(config);
                        } else {
                            sprintf(storePortText, "%d", (BONGO_STORE_DEFAULT_PORT));
                        }
			MDBAddValue(MsgGlobal.vs.server.names->Value[i], MsgGlobal.vs.storeDNs);
			for (j = count + 1; j < MsgGlobal.vs.server.contexts->Used; j++) {
			    MDBAddValue(addresses->Value[count], addresses);
                            MDBAddValue(storePortText, ports);
			    MDBAddValue(MsgGlobal.vs.server.names->Value[i], MsgGlobal.vs.storeDNs);
			}
                    }
                }
            }
        }

        /* Now check any aliases that might be in the Internet Services container */
        realUsed = MsgGlobal.vs.server.names->Used;
        if (MDBEnumerateObjects(MSGSRV_ROOT, C_ALIAS, NULL, MsgGlobal.vs.server.names)) {
            unsigned char realDn[MDB_MAX_OBJECT_CHARS + 1];
            unsigned char realType[MDB_MAX_ATTRIBUTE_CHARS + 1];

            /* Put the local context(s) first */
            for (i = realUsed; i < MsgGlobal.vs.server.names->Used; i++) {
                MDBGetObjectDetails(MsgGlobal.vs.server.names->Value[i], realType, NULL, realDn, MsgGlobal.vs.server.names);

                /* MsgGlobal.server.dn is absolute, MsgGlobal.vs.server.names->Value isn't */
                if ((XplStrCaseCmp(realType, MSGSRV_C_SERVER) == 0) && (XplStrCaseCmp(MsgGlobal.server.dn + strlen(MsgGlobal.server.dn) - strlen(realDn), realDn) != 0)) {
                    count = MsgGlobal.vs.server.contexts->Used;
                    if ((MDBRead(MsgGlobal.vs.server.names->Value[i], MSGSRV_A_IP_ADDRESS, addresses) > 0) &&
                        (MDBReadDN(MsgGlobal.vs.server.names->Value[i], MSGSRV_A_CONTEXT, MsgGlobal.vs.server.contexts) > 0)) {
                        sprintf(storeObjectDn, "%s\\%s", MsgGlobal.vs.server.names->Value[i], MSGSRV_AGENT_STORE);
                        if (MDBRead(storeObjectDn, MSGSRV_A_PORT, config)) {
                            strcpy(storePortText, config->Value[0]);
                            MDBFreeValues(config);
                        } else {
                            sprintf(storePortText, "%d", BONGO_STORE_DEFAULT_PORT);
                        }
			MDBAddValue(MsgGlobal.vs.server.names->Value[i], MsgGlobal.vs.storeDNs);
			for (j = count + 1; j < MsgGlobal.vs.server.contexts->Used; j++) {
			    MDBAddValue(addresses->Value[count], addresses);
                            MDBAddValue(storePortText, ports);
			    MDBAddValue(MsgGlobal.vs.server.names->Value[i], MsgGlobal.vs.storeDNs);
			}
                    }
                }
            }
        }
    }

    /* MDBAddValue doesn't add empty values, so fake it */
    /* with a ^A in the string */
    emptyString[0]=EMPTY_CHAR;
    /* Now read all the store path values */
    
    for (i = 0; i < MsgGlobal.vs.server.contexts->Used; i++) {
        if (MDBRead(MsgGlobal.vs.server.contexts->Value[i], MSGSRV_A_MESSAGE_STORE, MsgGlobal.vs.storePaths) == 0) {
            /* Add an empty string, no context store specified */
            MDBAddValue(emptyString, MsgGlobal.vs.storePaths);
        } else {
            MsgCleanPath(MsgGlobal.vs.storePaths->Value[i]);
        }
    }

    /* Initialize addr structure for each context */
    MsgGlobal.vs.server.addr = MemMalloc0(MsgGlobal.vs.server.contexts->Used * sizeof(struct sockaddr_in));
    if (!MsgGlobal.vs.server.addr) {
        MDBDestroyValueStruct(ports);
        return(FALSE);
    }

    for (i = 0; i < MsgGlobal.vs.server.contexts->Used; i++) {
        MsgGlobal.vs.server.addr[i].sin_addr.s_addr = inet_addr(addresses->Value[i]);
        MsgGlobal.vs.server.addr[i].sin_port = htons(atol(ports->Value[i]));
        MsgGlobal.vs.server.addr[i].sin_family = AF_INET;
    }


#if 0
    for (i = 0; i < MsgGlobal.vs.server.contexts->Used; i++) {
        XplConsolePrintf("\rCtx:%-35s IP:%-15s Path:%s\n", MsgGlobal.vs.server.contexts->Value[i], addresses->Value[i], MsgGlobal.vs.storePaths->Value[i]);
        XplConsolePrintf("\rDN :%-35s\n\n", MsgGlobal.vs.storeDNs->Value[i]);
    }
#endif

    MDBDestroyValueStruct(addresses);
    MDBDestroyValueStruct(ports);
    return(TRUE);
 }

static void
MsgConfigMonitor(void)
{
    MDBValueStruct *config;
    unsigned long i;
    long prevConfigNumber = 0;
    struct tm *timeStruct;
    time_t utcTime;
    long tmp;

    MsgGlobal.flags |= MSGAPI_FLAG_MONITOR_RUNNING;

    XplRenameThread(XplGetThreadID(), "MsgAPI Config Monitor");

    config = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);
    if (!config) {
        printf("WARNING: MsgConfigMonitor() couldn't create value struct: directory handle %p\n", 
               MsgGlobal.directoryHandle);
        return;
    }
    
    if (MDBRead(MSGSRV_ROOT, MSGSRV_A_CONFIG_CHANGED, config) > 0) {
        prevConfigNumber = atol(config->Value[0]);
    }

    while (!(MsgGlobal.flags & MSGAPI_FLAG_EXITING)) {
        for (i = 0; (i < 300) && !(MsgGlobal.flags & MSGAPI_FLAG_EXITING); i++) {
            XplDelay(1000);
        }

        /* Get Server Time Zone Offset */
        tzset();
        utcTime = time(NULL);
        timeStruct = localtime(&utcTime);
        if (timeStruct) {
            tmp = (((((((timeStruct->tm_year - 70) * 365) + timeStruct->tm_yday) * 24) + timeStruct->tm_hour) * 60) + timeStruct->tm_min) * 60;
            timeStruct = gmtime(&utcTime);
            tmp -= (((((((timeStruct->tm_year - 70) * 365) + timeStruct->tm_yday) * 24) + timeStruct->tm_hour) * 60) + timeStruct->tm_min) * 60;
            MsgDateSetUTCOffset(tmp); 
        }

        MDBFreeValues(config);

        if (!(MsgGlobal.flags & MSGAPI_FLAG_EXITING) && (MDBRead(MSGSRV_ROOT, MSGSRV_A_CONFIG_CHANGED, config)>0) && (atol(config->Value[0]) != prevConfigNumber)) {
            /* Clear what we just read */
            prevConfigNumber = atol(config->Value[0]);
            MDBFreeValues(config);
            
            /* Acquire Write Lock */            
            XplRWWriteLockAcquire(&MsgGlobal.configLock);

            if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_CONNMGR_CONFIG, config)) {
                if (atoi(config->Value[0]) == 1) {
                    MsgGlobal.flags |= MSGAPI_FLAG_SUPPORT_CONNMGR;
                }
            }
            MDBFreeValues(config);

            if (MsgReadIP(MsgGlobal.server.dn, MSGSRV_A_CONNMGR, config)) {
                MsgGlobal.connManager = inet_addr(config->Value[0]);
            }
            MDBFreeValues(config);

            LoadContextList(config);

            XplRWWriteLockRelease(&MsgGlobal.configLock);
        }
    }
    MDBDestroyValueStruct(config);
    
    MsgGlobal.flags &= ~MSGAPI_FLAG_MONITOR_RUNNING;

    XplExitThread(TSR_THREAD, 0);
}

static BOOL
MsgLibraryStop(void)
{
    while (MsgGlobal.flags & MSGAPI_FLAG_MONITOR_RUNNING) {
        XplDelay(1000);
    }

    MDBDestroyValueStruct(MsgGlobal.vs.storeDNs);
    MDBDestroyValueStruct(MsgGlobal.vs.storePaths);
    MDBDestroyValueStruct(MsgGlobal.vs.server.contexts);
    MDBDestroyValueStruct(MsgGlobal.vs.server.names);
    if (MsgGlobal.vs.server.addr) {
        MemFree(MsgGlobal.vs.server.addr);
        MsgGlobal.vs.server.addr = NULL;
    }


#ifdef PARENTOBJECT_STORE
    MDBDestroyValueStruct(MsgGlobal.vs.parent.stores);
    MDBDestroyValueStruct(MsgGlobal.vs.parent.objects);
#endif

    if (MsgGlobal.cache.handle) {
        MsgGlobal.cache.shutdown();

        XplReleaseDLLFunction(MsgGlobal.cache.fileName, "MsgGlobal.cache.init", MsgGlobal.cache.handle);
        XplReleaseDLLFunction(MsgGlobal.cache.fileName, "MsgGlobal.cache.shutdown", MsgGlobal.cache.handle);
        XplReleaseDLLFunction(MsgGlobal.cache.fileName, "FindObjectCache", MsgGlobal.cache.handle);
        XplReleaseDLLFunction(MsgGlobal.cache.fileName, "FindObjectStoreCache", MsgGlobal.cache.handle);

        XplUnloadDLL(MsgGlobal.cache.fileName, MsgGlobal.cache.handle);
    }

    XplRWLockDestroy(&MsgGlobal.configLock);

    return(TRUE);
}

/**
 * Get a config property from global configuration file.  Removes dependency 
 * on NCPServer object in the directory (as well as others). See msgapi.h for
 * possible properties.
 */
BOOL
MsgGetConfigProperty(unsigned char *Buffer, unsigned char *Property)
{
    int result = FALSE;
    char line[512];
    char *ptr;
    FILE *fh;
    int len;
    char conf_path[FILENAME_MAX];

    snprintf(conf_path, FILENAME_MAX, "%s/%s", XPL_DEFAULT_CONF_DIR, 
            MSGSRV_CONFIG_FILENAME);
    
    if (!(fh = fopen(conf_path, "r"))) {
        return result;
    }

    while (fgets(line, 512, fh)) {
        if (line[0] != '#') {
            if ((ptr = strchr(line, '='))) {
                *ptr++ = '\0';
                len = strlen(ptr);

                if (!strcmp(line, Property)) {
                    if (ptr[len - 1] == '\n') {
                        ptr[len - 1] = '\0';
                    }

                    strncpy(Buffer, ptr, MSGSRV_CONFIG_MAX_PROP_CHARS);
                    result = TRUE;
                    break;
                }
            } else {
                break;
            }
        }
    }

    fclose(fh);
    return result;
}

void
MsgGetUid(char *buffer, int buflen)
{
    static int serial = 0;
    static char hostname[512] = "";
    int thisSerial;

    XplMutexLock(MsgGlobal.sem.uid);
    
    if (hostname[0] == '\0') {
        if ((gethostname(hostname, sizeof(hostname) - 1) != 0) ||
            hostname[0] == '\0') {
            strcpy(hostname, "localhost");
        }
    }       

    thisSerial = serial++;
    
    XplMutexUnlock(MsgGlobal.sem.uid);
    
    snprintf(buffer, buflen, "%lu.%lu.%d@%s", 
             (unsigned long) time(NULL),
             (unsigned long) getpid(), 
             thisSerial, 
             hostname);
}

static BOOL
MsgLibraryStart(void)
{
    unsigned long i;
    struct sockaddr_in server_sockaddr;
    unsigned char path[XPL_MAX_PATH + 1];
    MDBValueStruct *config;
    XplThreadID ID;

    /* Prepare later config updates */
    XplRWLockInit(&MsgGlobal.configLock);

    /* Read generic configuration info */
    config = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);

    if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_CONNMGR_CONFIG, config)) {
        if (atoi(config->Value[0]) == 1) {
            MsgGlobal.flags |= MSGAPI_FLAG_SUPPORT_CONNMGR;
        }
    }
    MDBFreeValues(config);

    if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_CERTIFICATE_LOCATION, config)) {
        strcpy(MsgGlobal.paths.certificate, config->Value[0]);
        MsgCleanPath(MsgGlobal.paths.certificate);
    } else {
        if (MDBRead(MSGSRV_ROOT, MSGSRV_A_CERTIFICATE_LOCATION, config)) {
            strcpy(MsgGlobal.paths.certificate, config->Value[0]);
            MsgCleanPath(MsgGlobal.paths.certificate);
        } else {
            strcpy(MsgGlobal.paths.certificate, XPL_DEFAULT_CERT_PATH);
        }
    }
    MDBFreeValues(config);

    if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_PRIVATE_KEY_LOCATION, config)) {
        strcpy(MsgGlobal.paths.key, config->Value[0]);
        MsgCleanPath(MsgGlobal.paths.key);
    } else {
        if (MDBRead(MSGSRV_ROOT, MSGSRV_A_PRIVATE_KEY_LOCATION, config)) {
            strcpy(MsgGlobal.paths.key, config->Value[0]);
            MsgCleanPath(MsgGlobal.paths.key);
        } else {
            strcpy(MsgGlobal.paths.key, XPL_DEFAULT_KEY_PATH);
        }
    }
    MDBFreeValues(config);

    if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_CLUSTERED, config)>0) {
	if (atol (config->Value[0])) {
	    MsgGlobal.flags |= MSGAPI_FLAG_CLUSTERED;
	}
    }
    MDBFreeValues(config);
    if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_FORCE_BIND, config)>0) {
	if (atol (config->Value[0])) {
	    MsgGlobal.flags |= MSGAPI_FLAG_BOUND;
	}
    }
    MDBFreeValues(config);
    if (!(MsgGlobal.flags & MSGAPI_FLAG_CLUSTERED)) {
        MsgGlobal.flags &= ~MSGAPI_FLAG_BOUND;
    }
    /* Config is free'd further down */
    MDBFreeValues(config);

    /* Read "context" related stuff */

    MsgGlobal.vs.server.names = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);
    MsgGlobal.vs.server.contexts = MDBShareContext(MsgGlobal.vs.server.names);
    MsgGlobal.vs.storePaths = MDBShareContext(MsgGlobal.vs.server.names);
    MsgGlobal.vs.storeDNs = MDBShareContext(MsgGlobal.vs.server.names);
    MsgGlobal.vs.server.addr = NULL;

#ifdef PARENTOBJECT_STORE
    MsgGlobal.vs.parent.objects = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);
    MsgGlobal.vs.parent.stores = MDBShareContext(MsgGlobal.vs.parent.objects);
#endif

    server_sockaddr.sin_addr.s_addr=XplGetHostIPAddress();
    sprintf(MsgGlobal.address.string,"%d.%d.%d.%d",
	    server_sockaddr.sin_addr.s_net,
	    server_sockaddr.sin_addr.s_host,
	    server_sockaddr.sin_addr.s_lh,
	    server_sockaddr.sin_addr.s_impno);

    MDBAddValue(MsgGlobal.address.string, MsgGlobal.vs.server.names);
    if (!(MsgGlobal.flags & MSGAPI_FLAG_CLUSTERED)) {
        MDBWrite(MsgGlobal.server.dn, MSGSRV_A_IP_ADDRESS, MsgGlobal.vs.server.names);
    } else {
        MDBFreeValues(MsgGlobal.vs.server.names);
        if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_IP_ADDRESS, MsgGlobal.vs.server.names)>0) {
            strcpy(MsgGlobal.address.string, MsgGlobal.vs.server.names->Value[0]);
        }
    }

    MDBFreeValues(MsgGlobal.vs.server.names);
    MsgGlobal.address.local = inet_addr(MsgGlobal.address.string);

    /* This stuff is here to prevent a catch-22 with the IP address configuration above */
    if (MsgReadIP(MsgGlobal.server.dn, MSGSRV_A_CONNMGR, config)) {
        MsgGlobal.connManager = inet_addr(config->Value[0]);
    }
    MDBFreeValues(config);

    LoadContextList(config);

    MDBDestroyValueStruct(config);

    XplBeginThread(&ID, MsgConfigMonitor, 32767, NULL, i);

    /*    Attempt to load the plugable cache mechanism    */
    sprintf(MsgGlobal.cache.fileName, "%s%s", MsgGlobal.cache.moduleName, XPL_DLL_EXTENSION);
    sprintf(path, "%s/%s", XPL_DEFAULT_BIN_DIR, MsgGlobal.cache.fileName);

    MsgGlobal.cache.handle = XplLoadDLL(path);
    if (MsgGlobal.cache.handle != NULL) {
        MsgGlobal.cache.init = (FindObjectCacheInitType)XplGetDLLFunction(MsgGlobal.cache.fileName, "MsgGlobal.cache.init", MsgGlobal.cache.handle);
        MsgGlobal.cache.shutdown = (FindObjectCacheShutdownType)XplGetDLLFunction(MsgGlobal.cache.fileName, "MsgGlobal.cache.shutdown", MsgGlobal.cache.handle);
        MsgGlobal.cache.find = (FindObjectCacheType)XplGetDLLFunction(MsgGlobal.cache.fileName, "FindObjectCache", MsgGlobal.cache.handle);
        MsgGlobal.cache.findEx = (FindObjectCacheExType)XplGetDLLFunction(MsgGlobal.cache.fileName, "FindObjectExCache", MsgGlobal.cache.handle);
        MsgGlobal.cache.store = (FindObjectStoreCacheType)XplGetDLLFunction(MsgGlobal.cache.fileName, "FindObjectStoreCache", MsgGlobal.cache.handle);

        if (!MsgGlobal.cache.init || !MsgGlobal.cache.shutdown || !MsgGlobal.cache.find || !MsgGlobal.cache.findEx || !MsgGlobal.cache.store) {
            MsgGlobal.cache.init = NULL;
            MsgGlobal.cache.shutdown = NULL;
            MsgGlobal.cache.find = NULL;
            MsgGlobal.cache.findEx = NULL;
            MsgGlobal.cache.store = NULL;
        }

        if (MsgGlobal.cache.init) {
            MSGCacheInitStruct initData;

            initData.DirectoryHandle = MsgGlobal.directoryHandle;
            initData.ServerContexts = MsgGlobal.vs.server.contexts;
            initData.ServerAddr = MsgGlobal.vs.server.addr;
            initData.StorePath = MsgGlobal.vs.storePaths;
            initData.ConfigLock = &MsgGlobal.configLock;
            initData.DefaultFindObject = MsgFindObject;
            initData.DefaultFindObjectEx = MsgFindObjectEx;
            initData.DefaultFindObjectStore = MsgFindUserStore;
            initData.DefaultPathChar = EMPTY_CHAR;

	    if (!MsgGlobal.cache.init(&initData, path)) {
                MsgGlobal.cache.init = NULL;
                MsgGlobal.cache.shutdown = NULL;
                MsgGlobal.cache.find = NULL;
                MsgGlobal.cache.findEx = NULL;
                MsgGlobal.cache.store = NULL;
            }

        }
    }

    return(TRUE);
}

MDBHandle 
MsgGetSystemDirectoryHandle(void)
{
    MDBHandle systemHandle;
    unsigned char buffer[XPL_MAX_PATH + 1];
    unsigned char credential[128];
    FILE *eclients;

    memset(credential, 0, sizeof(credential));

    sprintf(buffer, "%s/eclients.dat", XPL_DEFAULT_DBF_DIR);
    eclients = fopen(buffer, "rb");
    if (eclients) {
        fread(credential, sizeof(unsigned char), sizeof(credential), eclients);

        fclose(eclients);
        eclients = NULL;
    } else {
        XplConsolePrintf("Insufficient privileges; shutting down.\n");
        return(NULL);
    }

    if (!MsgGetConfigProperty(MsgGlobal.server.dn,
	                      MSGSRV_CONFIG_PROP_MESSAGING_SERVER)) {
        XplConsolePrintf("Messaging server not configured. Shutdown.\n");
        return(NULL);
    } 

    systemHandle = MDBAuthenticate("Bongo", MsgGlobal.server.dn, credential);
    if (systemHandle == NULL) {
        XplConsolePrintf("Messaging server credentials are invalid; shutting down.\n");
        return(NULL);
    }

    return(systemHandle);
}

BOOL
MsgGetBuildVersion(int *version, BOOL *custom)
{
    /* Get the version of this build of bongo.
     * version - what SVN rev or branch minor this is
     * custom -  whether or not this is a custom build
     */
    int len = 0;
    char *ptr = BONGO_BUILD_VER;

    *version = strtol(ptr, NULL, 10);
    len = strlen(ptr);
    
    if (ptr[len] == 'M') *custom = TRUE;
    return TRUE;
}

BOOL
MsgGetAvailableVersion(int *version)
{
    /* Get the information about latest version of bongo available
     * version - what SVN rev or branch minor is available
     */
    char record[500];
    char *ptr;
    int v;

    if (!MsgGetUpdateStatus(record, 499))
        return FALSE;

    ptr = strchr(record, '|');
    if (ptr == NULL) return FALSE;

    *version = strtol(record, &ptr, 10);
    return TRUE;
}

BOOL
MsgGetUpdateStatus(char *record, int record_length)
{
    char *branch = BONGO_BUILD_BRANCH;
    char *zone = "revisions.bongo-project.com";
    char hostname[256];
    XplDnsRecord *result = NULL;
    int status;

    snprintf(hostname, 255, "%s.%s", branch, zone);
    
    status = XplDnsResolve(hostname, &result, XPL_RR_TXT);
    switch(status) {
        case XPL_DNS_SUCCESS:
            snprintf(record, record_length, "%s", result[0].TXT.txt);
            MemFree(result);
            return TRUE;
            break;
        default:
            return FALSE;
    }
}

static BOOL
MsgReadConfiguration(void)
{
    MDBValueStruct *config;
    unsigned long i;
    struct tm *timeStruct;
    time_t utcTime;
    long tmp;

    /* Get Server Time Zone Offset */
    tzset();
    utcTime = time(NULL);
    timeStruct = localtime(&utcTime);
    if (timeStruct) {
        tmp = (((((((timeStruct->tm_year - 70) * 365) + timeStruct->tm_yday) * 24) + timeStruct->tm_hour) * 60) + timeStruct->tm_min) * 60;
        timeStruct = gmtime(&utcTime);
        tmp -=    (((((((timeStruct->tm_year - 70) * 365) + timeStruct->tm_yday) * 24) + timeStruct->tm_hour) * 60) + timeStruct->tm_min) * 60;

        MsgDateSetUTCOffset(tmp); 
    }

    /* Read operating parameters, this is so complicated because in version 2.5
     * we changed the configuration of directories - before 2.5 we would always
     * append novonyx/mail to any given path, now we don't. The code tries to
     * automatically detect pre2.5 installs and change the DS attribute...
     */

    config = MDBCreateValueStruct(MsgGlobal.directoryHandle, NULL);

    if (!config) {
        XplConsolePrintf("Messaging server out of memory; shutting down.\n");
        MDBRelease(MsgGlobal.directoryHandle);
        MsgGlobal.directoryHandle = NULL;
        return(FALSE);
    }

    if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_NLS_DIRECTORY, config)) {
        strcpy(MsgGlobal.paths.nls, config->Value[0]);
        MsgCleanPath(MsgGlobal.paths.nls);
        MsgMakePath(MsgGlobal.paths.nls);
        MDBFreeValues(config);
        if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_DBF_DIRECTORY, config)) {
            strcpy(MsgGlobal.paths.dbf, config->Value[0]);
            MsgCleanPath(MsgGlobal.paths.dbf);
            MsgMakePath(MsgGlobal.paths.dbf);
            MDBFreeValues(config);
        } else {
            strcpy(MsgGlobal.paths.dbf, XPL_DEFAULT_DBF_DIR);
            MsgMakePath(MsgGlobal.paths.dbf);
        }

        if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_BIN_DIRECTORY, config)) {
            strcpy(MsgGlobal.paths.bin, config->Value[0]);
            MsgCleanPath(MsgGlobal.paths.bin);
            MsgMakePath(MsgGlobal.paths.bin);
            MDBFreeValues(config);
        } else {
            strcpy(MsgGlobal.paths.bin, XPL_DEFAULT_BIN_DIR);
            MsgMakePath(MsgGlobal.paths.bin);
        }

        if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_LIB_DIRECTORY, config)) {
            strcpy(MsgGlobal.paths.lib, config->Value[0]);
            MsgCleanPath(MsgGlobal.paths.lib);
            MsgMakePath(MsgGlobal.paths.lib);
            MDBFreeValues(config);
        } else {
            strcpy(MsgGlobal.paths.lib, XPL_DEFAULT_LIB_DIR);
            MsgMakePath(MsgGlobal.paths.lib);
        }

        if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_WORK_DIRECTORY, config)) {
            strcpy(MsgGlobal.paths.work, config->Value[0]);
            MsgCleanPath(MsgGlobal.paths.work);
            MsgMakePath(MsgGlobal.paths.work);
            MDBFreeValues(config);
        } else {
            strcpy(MsgGlobal.paths.work, XPL_DEFAULT_WORK_DIR);
            MsgMakePath(MsgGlobal.paths.work);
        }
    } else {
        strcpy(MsgGlobal.paths.dbf, XPL_DEFAULT_DBF_DIR);
        MsgMakePath(MsgGlobal.paths.dbf);
        MDBAddValue(MsgGlobal.paths.dbf, config);
        MDBWrite(MsgGlobal.server.dn, MSGSRV_A_DBF_DIRECTORY, config);
        MDBFreeValues(config);

        strcpy(MsgGlobal.paths.nls, XPL_DEFAULT_NLS_DIR);
        MsgMakePath(MsgGlobal.paths.nls);
        MDBAddValue(MsgGlobal.paths.nls, config);
        MDBWrite(MsgGlobal.server.dn, MSGSRV_A_NLS_DIRECTORY, config);
        MDBFreeValues(config);

        strcpy(MsgGlobal.paths.bin, XPL_DEFAULT_BIN_DIR);
        MsgMakePath(MsgGlobal.paths.bin);
        MDBAddValue(MsgGlobal.paths.bin, config);
        MDBWrite(MsgGlobal.server.dn, MSGSRV_A_BIN_DIRECTORY, config);
        MDBFreeValues(config);

        if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_WORK_DIRECTORY, config)) {
            strcpy(MsgGlobal.paths.work, config->Value[0]);
            MsgCleanPath(MsgGlobal.paths.work);
            strcat(MsgGlobal.paths.work, "/novonyx/mail");
            MsgMakePath(MsgGlobal.paths.work);
            MDBFreeValues(config);

            MDBAddValue(MsgGlobal.paths.work, config);
            MDBWrite(MsgGlobal.server.dn, MSGSRV_A_WORK_DIRECTORY, config);
            MDBFreeValues(config);
        } else {
            strcpy(MsgGlobal.paths.work, XPL_DEFAULT_WORK_DIR);
            MsgMakePath(MsgGlobal.paths.work);
            MDBAddValue(MsgGlobal.paths.work, config);
            MDBWrite(MsgGlobal.server.dn, MSGSRV_A_WORK_DIRECTORY, config);
            MDBFreeValues(config);
        }
    }

    /* Official Name */
    if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_OFFICIAL_NAME, config)) {
        strcpy(MsgGlobal.official.domain, config->Value[0]);
        MsgGlobal.official.domainLength = strlen(MsgGlobal.official.domain);
    }
    MDBFreeValues(config);

    if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_CONFIGURATION, config)) {
        for (i = 0; i < config->Used; i++) {
            if (XplStrNCaseCmp(config->Value[i], "FindObjectModule=", 17) == 0) {
                strcpy(MsgGlobal.cache.moduleName, config->Value[i]+17);
            } else if (XplStrNCaseCmp(config->Value[i], "IgnoreInternetEmailAddressAttribute", 35) == 0) {
                MsgGlobal.flags |= MSGAPI_FLAG_INTERNET_EMAIL_ADDRESS;
            }
        }
    }
    MDBFreeValues(config);

    if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_SERVER_STANDALONE, config)) {
        if (config->Value[0][0]=='1') {
            MsgGlobal.flags |= MSGAPI_FLAG_STANDALONE;
        }
    }
    MDBDestroyValueStruct(config);

    return(TRUE);
}

static int
MsgLibraryInit(void)
{
    MsgGlobal.flags = 0;

    XplSafeWrite(MsgGlobal.useCount, 0);

    MsgGlobal.groupID = XplGetThreadGroupID();

    MsgGlobal.directoryHandle = NULL;

    MsgGlobal.connManager = 0x0100007F;

    strcpy(MsgGlobal.cache.moduleName, "msgcache");
    MsgGlobal.cache.init = NULL;
    MsgGlobal.cache.shutdown = NULL;
    MsgGlobal.cache.find = NULL;
    MsgGlobal.cache.findEx = NULL;
    MsgGlobal.cache.store = NULL;

    MsgGlobal.official.domain[0] = '\0';
    MsgGlobal.official.domainLength = 0;

    XplOpenLocalSemaphore(MsgGlobal.sem.shutdown, 0);
    XplMutexInit(MsgGlobal.sem.uid);

    MemoryManagerOpen(NULL);

    if (!MDBInit()) {
        MemoryManagerClose(NULL);
        return 0;
    }

    MsgGlobal.directoryHandle = MsgGetSystemDirectoryHandle();
    if (!(MsgGlobal.directoryHandle)) {
        MemoryManagerClose(NULL);
        return 0;        
    }

    if (!MsgReadConfiguration()) {
        XplConsolePrintf("Cannot read configuration. Shutting down.\n");

        MemoryManagerClose(NULL);

        return(0);
    }

    MsgResolveStart();
    MsgLibraryStart();
    MsgDateStart();

    if (curl_global_init(CURL_GLOBAL_ALL)) {
        XplConsolePrintf("Could not initialize curl.\r\n");
        return 0;
    }

    MSGAPIState = LIBRARY_RUNNING;

    return 1;
}

static void 
MsgLibraryShutdown(void)
{
    XplThreadID oldGid;

    if (MSGAPIState == LIBRARY_RUNNING) {
        MSGAPIState = LIBRARY_SHUTTING_DOWN;

        MsgGlobal.flags |= MSGAPI_FLAG_EXITING;

        oldGid = XplSetThreadGroupID(MsgGlobal.groupID);

        MsgResolveStop();
        MsgLibraryStop();

        MemoryManagerClose(NULL);

        MSGAPIState = LIBRARY_SHUTDOWN;

        XplSetThreadGroupID(oldGid);
    }

    return;
}

MDBHandle 
MsgDirectoryHandle(void)
{
    return(MsgGlobal.directoryHandle);
}

BOOL
MsgResolveStart(void)
{
    MDBValueStruct *config;
    unsigned long i;

    if (!XplResolveStart()) {
	return FALSE;
    }
    
    config = MDBCreateValueStruct(MsgDirectoryHandle(), NULL);
    if (MDBRead(MsgGetServerDN(NULL), MSGSRV_A_RESOLVER, config) > 0) {
        for (i = 0; i < config->Used; i++) {
            XplDnsAddResolver(config->Value[i]);
/*            XplConsolePrintf("[%04d] MSGSRV_A_RESOLVER:%s", __LINE__, config->Value[i]);*/
        }
    } else {
        MDBSetValueStructContext(MsgGetServerDN(NULL), config);
        if (MDBRead(MSGSRV_AGENT_SMTP, MSGSRV_A_RESOLVER, config)>0) {
            for (i = 0; i < config->Used; i++) {
                XplDnsAddResolver(config->Value[i]);
/*		XplConsolePrintf("[%04d] MSGSRV_A_RESOLVER:%s", __LINE__, config->Value[i]);*/
            }
            MDBSetValueStructContext(NULL, config);
            MDBWrite(MsgGetServerDN(NULL), MSGSRV_A_RESOLVER, config);
        }
    }
    MDBDestroyValueStruct(config);

    return(TRUE);
}

BOOL
MsgResolveStop(void)
{
    return XplResolveStop();
}



BOOL 
MsgExiting(void)
{
    if (MsgGlobal.flags & MSGAPI_FLAG_EXITING) {
        return(TRUE);
    }

    return(FALSE);
}

EXPORT BOOL 
MsgShutdown(void)
{
    XplSafeDecrement(MsgGlobal.useCount);

    if (XplSafeRead(MsgGlobal.useCount) > 0) {
        return(FALSE);
    }

    MsgLibraryShutdown();

    return(TRUE);
}

EXPORT MDBHandle 
MsgInit(void)
{
    if (MSGAPIState == LIBRARY_LOADED) {
        MSGAPIState = LIBRARY_INITIALIZING;

        if (!MsgLibraryInit()) {
            return NULL;
        }
    }

    while (MSGAPIState < LIBRARY_RUNNING) {
        XplDelay(100);
    }

    if (MSGAPIState == LIBRARY_RUNNING) {
        XplSafeIncrement(MsgGlobal.useCount);
    }

    return((MSGAPIState == LIBRARY_RUNNING) ? MsgGlobal.directoryHandle : NULL);
}
