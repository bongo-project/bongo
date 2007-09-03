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

    XplThreadID groupID;

    struct {
        XplSemaphore version;
        XplSemaphore shutdown;
        XplMutex uid;
    } sem;

    unsigned long connManager;

    XplRWLock configLock;
    
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
	// DEPRECATED
	return(NULL);
}

EXPORT BOOL
MsgDomainExists(const unsigned char *domain, unsigned char *domainObjectDN)
{
	// DEPRECATED ?
    /* FIXME later; when we handle parent objects */
    return(FALSE);
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
MsgGetDir(MsgApiDirectory directory, char *buffer, size_t buffer_size)
{
	const unsigned char *path;
	switch(directory) {
		case MSGAPI_DIR_BIN:
			path = XPL_DEFAULT_BIN_DIR;
			break;
		case MSGAPI_DIR_CACHE:
			path = XPL_DEFAULT_CACHE_DIR;
			break;
		case MSGAPI_DIR_CERT:
			path = XPL_DEFAULT_DBF_DIR;
			break;
		case MSGAPI_DIR_DATA:
			path = XPL_DEFAULT_DATA_DIR;
			break;
		case MSGAPI_DIR_DBF:
			path = XPL_DEFAULT_DBF_DIR;
			break;
		case MSGAPI_DIR_LIB:
			path = XPL_DEFAULT_LIB_DIR;
			break;
		case MSGAPI_DIR_MAIL:
			path = XPL_DEFAULT_MAIL_DIR;
			break;
		case MSGAPI_DIR_SCMS:
			path = XPL_DEFAULT_SCMS_DIR;
			break;
		case MSGAPI_DIR_SPOOL:
			path = XPL_DEFAULT_SPOOL_DIR;
			break;
		case MSGAPI_DIR_STATE:
			path = XPL_DEFAULT_STATE_DIR;
			break;
		case MSGAPI_DIR_STORESYSTEM:
			path = XPL_DEFAULT_STORE_SYSTEM_DIR;
			break;
		case MSGAPI_DIR_WORK:
			path = XPL_DEFAULT_WORK_DIR;
			break;
		default:
			if (buffer && buffer_size)
				buffer[0] = '\0';
			return NULL;
	}
	if (buffer) {
		strncpy(buffer, path, buffer_size - 1);
	}
	return path;
}

EXPORT const unsigned char *
MsgGetFile(MsgApiFile file, char *buffer, size_t buffer_size)
{
	const unsigned char *path;
	switch(file) {
		case MSGAPI_FILE_PUBKEY:
			path = XPL_DEFAULT_CERT_PATH;
			break;
		case MSGAPI_FILE_PRIVKEY:
			path = XPL_DEFAULT_KEY_PATH;
			break;
		case MSGAPI_FILE_DHPARAMS:
			path = XPL_DEFAULT_DHPARAMS_PATH;
			break;
		case MSGAPI_FILE_RSAPARAMS:
			path = XPL_DEFAULT_RSAPARAMS_PATH;
			break;
		case MSGAPI_FILE_RANDSEED:
			path = XPL_DEFAULT_RANDSEED_PATH;
			break;
		default:
			if (buffer && buffer_size)
				buffer[0] = '\0';
			return NULL;
	}
	if (buffer) {
		strncpy(buffer, path, buffer_size - 1);
	}
	return path;
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
MsgLibraryStop(void)
{
    while (MsgGlobal.flags & MSGAPI_FLAG_MONITOR_RUNNING) {
        XplDelay(1000);
    }

    XplRWLockDestroy(&MsgGlobal.configLock);

    return(TRUE);
}

#if 0
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

    snprintf(conf_path, FILENAME_MAX, "%s/bongo.conf", XPL_DEFAULT_CONF_DIR);
    
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
#endif 

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
    XplThreadID ID;

    /* Prepare later config updates */
    XplRWLockInit(&MsgGlobal.configLock);

    /* Read generic configuration info */
    MsgGlobal.flags |= MSGAPI_FLAG_SUPPORT_CONNMGR;
    // MsgGlobal.flags |= MSGAPI_FLAG_CLUSTERED;
    // 'BOUND' conflicts with 'CLUSTERED' ?
    // MsgGlobal.flags |= MSGAPI_FLAG_BOUND; 

    return(TRUE);
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

    return(TRUE);
}

static int
MsgLibraryInit(void)
{
    MsgGlobal.flags = 0;

    XplSafeWrite(MsgGlobal.useCount, 0);

    MsgGlobal.groupID = XplGetThreadGroupID();

    MsgGlobal.connManager = 0x0100007F;

    MsgGlobal.official.domain[0] = '\0';
    MsgGlobal.official.domainLength = 0;

    XplOpenLocalSemaphore(MsgGlobal.sem.shutdown, 0);
    XplMutexInit(MsgGlobal.sem.uid);

    MemoryManagerOpen(NULL);

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

EXPORT void
MsgInit(void)
{
    if (MSGAPIState == LIBRARY_LOADED) {
        MSGAPIState = LIBRARY_INITIALIZING;

        if (!MsgLibraryInit()) return;
    }

    while (MSGAPIState < LIBRARY_RUNNING) {
        XplDelay(100);
    }

    if (MSGAPIState == LIBRARY_RUNNING) {
        XplSafeIncrement(MsgGlobal.useCount);
    }
}

void
MsgRecoveryFlagFile(char *agent, char *buffer, size_t buffer_len)
{
	snprintf(buffer, buffer_len - 1, "%s/%s.pid", XPL_DEFAULT_WORK_DIR, agent);
}

EXPORT BOOL
MsgSetRecoveryFlag(unsigned char *agent_name)
{
	FILE *flag;
	unsigned char flagfile[XPL_MAX_PATH];
	unsigned char pidstr[20];
	pid_t pid;
	
	MsgRecoveryFlagFile(agent_name, flagfile, XPL_MAX_PATH);
	
	flag = fopen(flagfile, "w");
	if (flag == -1) {
		return FALSE;
	}
	
	snprintf(pidstr, 18, "%lu\n", getpid());
	fwrite(pidstr, strlen(pidstr), sizeof(unsigned char), flag);
	
	fclose(flag);
	
	return TRUE;
}

EXPORT BOOL
MsgGetRecoveryFlag(unsigned char *agent_name)
{
	unsigned char flagfile[XPL_MAX_PATH];
	struct stat filestat;
	
	MsgRecoveryFlagFile(agent_name, flagfile, XPL_MAX_PATH);
	if (stat(flagfile, &filestat) == 0) {
		return TRUE;
	} else {
		return FALSE;
	}
}

EXPORT BOOL
MsgClearRecoveryFlag(unsigned char *agent_name)
{
	unsigned char flagfile[XPL_MAX_PATH];
	
	MsgRecoveryFlagFile(agent_name, flagfile, XPL_MAX_PATH);
	unlink(flagfile);
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

EXPORT BOOL
MsgSetServerCredential()
{
	unsigned char credential[4097];
	unsigned char path[XPL_MAX_PATH];
	FILE *credfile;
	const char *posschars = 
		"abcdefghijlkmnopqrstuvwxyz"
		"ABCDEFGHIJKLMNOPQRSTUVWYXZ"
		"0123456890";
	int i, range;
	
	range = strlen(posschars);
	for (i=0; i<4097; i++) {
		int ran = rand() % range;
		credential[i] = posschars[ran];
	}
	credential[4096] = '\0';
	
	snprintf(path, XPL_MAX_PATH, "%s/credential.dat", XPL_DEFAULT_DBF_DIR);
	credfile = fopen(path, "wb");
	if (credfile) {
		fwrite(credential, sizeof(char), sizeof(credential), credfile);
		fclose(credfile);
		return TRUE;
	}
	return FALSE;
}
