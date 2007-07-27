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

/* private */
BOOL
MsgGetUpdateStatus(char *record, int record_length);

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
    xpl_hash_context ctx; 	 
 	 
    MsgGetServerCredential(&access);
 	 
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
MsgFindObject(const unsigned char *user, unsigned char *dn, unsigned char *userType, struct sockaddr_in *nmap, MDBValueStruct *v)
{
    BOOL retVal = FALSE;

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

    return(retVal);
}

EXPORT BOOL
MsgDomainExists(const unsigned char *domain, unsigned char *domainObjectDN)
{
    /* FIXME later; when we handle parent objects */
    return(FALSE);
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
    MsgGlobal.flags |= MSGAPI_FLAG_SUPPORT_CONNMGR;
    // MsgGlobal.flags |= MSGAPI_FLAG_CLUSTERED;
    // 'BOUND' conflicts with 'CLUSTERED' ?
    // MsgGlobal.flags |= MSGAPI_FLAG_BOUND; 

    strcpy(MsgGlobal.paths.certificate, XPL_DEFAULT_CERT_PATH);
    strcpy(MsgGlobal.paths.key, XPL_DEFAULT_KEY_PATH);

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

    // MDBFreeValues(config);

    //LoadContextList(config);

    // MDBDestroyValueStruct(config);

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
   
    *custom = FALSE; 
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

    strcpy(MsgGlobal.paths.dbf, XPL_DEFAULT_DBF_DIR);
    MsgMakePath(MsgGlobal.paths.dbf);
    strcpy(MsgGlobal.paths.bin, XPL_DEFAULT_BIN_DIR);
    MsgMakePath(MsgGlobal.paths.bin);
    strcpy(MsgGlobal.paths.lib, XPL_DEFAULT_LIB_DIR);
    MsgMakePath(MsgGlobal.paths.lib);
    strcpy(MsgGlobal.paths.work, XPL_DEFAULT_WORK_DIR);
    MsgMakePath(MsgGlobal.paths.work);
    strcpy(MsgGlobal.paths.nls, XPL_DEFAULT_NLS_DIR);
    MsgMakePath(MsgGlobal.paths.nls);
    strcpy(MsgGlobal.paths.work, XPL_DEFAULT_WORK_DIR);
    MsgMakePath(MsgGlobal.paths.work);

    /* Official Name */
    if (MDBRead(MsgGlobal.server.dn, MSGSRV_A_OFFICIAL_NAME, config)) {
        strcpy(MsgGlobal.official.domain, config->Value[0]);
        MsgGlobal.official.domainLength = strlen(MsgGlobal.official.domain);
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

EXPORT BOOL
MsgSetRecoveryFlag(void)
{
	// FIXME: TODO :)
	return FALSE;
}

EXPORT BOOL
MsgGetRecoveryFlag(void)
{
	// FIXME: TODO :)
	return FALSE;
}

EXPORT BOOL
MsgGetServerCredential(char *buffer)
{
	unsigned char credential[4097];
	unsigned char file[120];
	FILE *credfile;

	memset(credential, 0, sizeof(credential));

	sprintf(file, "%s/credential.dat", XPL_DEFAULT_DBF_DIR);
	credfile = fopen(file, "rb");
	if (credfile) {
		fread(credential, sizeof(unsigned char), sizeof(credential), credfile);
		fclose(credfile);
		credfile = NULL;
		HashCredential(credential, buffer);
		return TRUE;
	}
	return FALSE;
}
