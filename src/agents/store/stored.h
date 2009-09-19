/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005, 2006 Novell, Inc. All Rights Reserved.
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

#ifndef _STORED_H
#define _STORED_H

#define LOGGERNAME "store"
#define _NO_BONGO_GLOBALS "yes"

#include <config.h>
#include <xpl.h>
#include <msgapi.h>

XPL_BEGIN_C_LINKAGE

#include <bongoutil.h>
#include <connio.h>
#include <msgapi.h>
#include <bongoagent.h>
#include <nmap.h>
#include <bongostore.h>

#include <bongojson.h>
#include <bongocal.h>

#include <logger.h>

#include "properties.h"

/** Store stuff: **/

#define GUID_FMT STORE_GUID_FMT

#define DATETIME_LEN 20
#define STORE_MAX_TOKEN_LEN 256

#define STORE_INVALID_GUID 0
#define STORE_ROOT_GUID 1
#define STORE_IS_FOLDER(doctype) (doctype & STORE_DOCTYPE_FOLDER)
#define STORE_IS_DBONLY(doctype) ((doctype == STORE_DOCTYPE_CONVERSATION) || (doctype == STORE_DOCTYPE_CALENDAR))
#define STORE_IS_CONVERSATION(doctype) (doctype == STORE_DOCTYPE_CONVERSATION)

typedef enum {
    CONVERSATION_SOURCE_ALL = (1 << 0),
    CONVERSATION_SOURCE_INBOX = (1 << 1),
    CONVERSATION_SOURCE_TRASH = (1 << 2),
    CONVERSATION_SOURCE_JUNK = (1 << 3),
    CONVERSATION_SOURCE_STARRED = (1 << 4),
    CONVERSATION_SOURCE_SENT = (1 << 5),
    CONVERSATION_SOURCE_DRAFTS = (1 << 6),
} ConversationSource;

/* Store Header Info stuff
   an array of these objects represents a header query in conjunctive normal form.
*/

typedef enum {
    HEADER_INFO_OR   = 0,
    HEADER_INFO_NOT  = 1,
    HEADER_INFO_AND  = 2,
} StoreHeaderInfoFlags;

typedef struct {
    enum { 
        HEADER_INFO_FROM,
        HEADER_INFO_RECIP,    /* not supported */
        HEADER_INFO_LISTID,
        HEADER_INFO_SUBJECT,
    } type;
    
    int flags;
    char *value;
} StoreHeaderInfo;

/** Store agent stuff: **/

#define AGENT_NAME "bongostore"
#define AGENT_DN "StoreAgent"

#define STORE_CLIENT_WATCH_JOURNAL_LEN 64

typedef enum {
    STORE_CLIENT_FLAG_MANAGER =       1 << 0,     /* superuser      */
    STORE_CLIENT_FLAG_IDENTITY =      1 << 1,     /* user           */
    STORE_CLIENT_FLAG_STORE =         1 << 2,     /* store attached */
    STORE_CLIENT_FLAG_NEEDS_COMPACTING = 1 << 3,
    STORE_CLIENT_FLAG_DONT_CACHE =    1 << 4,
} StoreClientFlags;


#define IS_MANAGER(_client) (STORE_CLIENT_FLAG_MANAGER & _client->flags)
#define HAS_IDENTITY(_client) (STORE_CLIENT_FLAG_IDENTITY & _client->flags)
#define HAS_STORE(_client) (STORE_CLIENT_FLAG_STORE & _client->flags)

typedef enum {
    STORE_WATCH_EVENT_NEW =      1 << 0,
    STORE_WATCH_EVENT_MODIFIED = 1 << 1,
    STORE_WATCH_EVENT_DELETED =  1 << 2,
    STORE_WATCH_EVENT_FLAGS =    1 << 3,

    STORE_WATCH_EVENT_COLL_RENAMED = 1 << 8,
    STORE_WATCH_EVENT_COLL_REMOVED = 1 << 9,
} StoreWatchEvents;

typedef struct _StoreToken {
    char data[STORE_MAX_TOKEN_LEN];
    struct _StoreToken *next;
} StoreToken;

typedef struct _StoreClient StoreClient;

#include "lock.h"

typedef struct _TimedMessage {
	struct _TimedMessage *next;
	struct timeval time;
	char *message;
} TimedMessage;

typedef struct _TimedCommand {
	TimedMessage *msgs;
	TimedMessage *lastmsg;
	char *command;
	struct timeval start;
	struct timeval end;
} TimedCommand;

struct _StoreClient {
    Connection *conn;
    unsigned int flags;
    MsgSQLHandle *storedb;
    BongoMemStack memstack;
    int lockTimeoutMs;
    TimedCommand *tc;

    char buffer[CONN_BUFSIZE + 1];

    /* the following vars are initialized by the STORE command: */
    const char *storeRoot;
    unsigned long storeHash;         /* hash of store, should be uint32_t  */
    char store[XPL_MAX_PATH + 1];    /* "/storeroot/store/" */
    char *storeName;                 /* "store" */
    BOOL readonly;                   // are we in read-only mode
    char const *ro_reason;                 // the reason we're read-only

    /* the principal is initialized by the USER command: */
    struct {
        char name[MAXEMAILNAMESIZE + 1];
        StorePrincipalType type;
        StoreToken *tokens;
    } principal;

    char authToken[64];
    
    struct {
        uint64_t collection;
        uint32_t flags;
        
        struct {
            uint64_t guids[STORE_CLIENT_WATCH_JOURNAL_LEN];
            uint32_t imapuids[STORE_CLIENT_WATCH_JOURNAL_LEN];
            uint32_t flags[STORE_CLIENT_WATCH_JOURNAL_LEN];
            int events[STORE_CLIENT_WATCH_JOURNAL_LEN];
            int count;
        } journal;
    } watch;

    NLockStruct *watchLock;

    /* zeroed by STORE command: */
    struct {
        int insertions;
        int updates;
        int deletions;
    } stats;
};

struct StoreGlobals {
    BongoAgent agent;

    XplThreadID threadGroup;    

    Connection *nmapConn; /* this is the conn we're listening on */
    char nmapAddress[80];
    
    void *memPool;
    BongoThreadPool *threadPool;

    enum {
        STORE_DISK_SPACE_LOW = 1, 
        STORE_DEBUG = 2, 
    } flags;


    time_t startupTime;
    BOOL installMode;
    XplRWLock configLock;

    struct {
        void *logging;
    } handle;

    struct {
        struct {
            BOOL enable;
            ConnSSLConfiguration config;
            bongo_ssl_context *context;
            Connection *conn;
        } ssl;

        BOOL bound;

        Connection *conn;   /*  NMAPServerSocket */

        XplAtomic active;

        int maxClients;
        unsigned long ipAddress;
        unsigned long bytesPerBlock;

        char host[MAXEMAILNAMESIZE + 1];
        char hash[NMAP_HASH_SIZE];
    } server;

    struct {
        char rootDir[XPL_MAX_PATH + 1];
        char spoolDir[XPL_MAX_PATH + 1];
        char systemDir[XPL_MAX_PATH + 1];
        /* FIXME: need a better name for this var: */
        int incomingQueueBytes;  /* max size of "incoming" file */ 
        int lockTimeoutMs;
    } store;

    struct {
        BongoHashtable *entries;
        XplMutex lock;
        int capacity;
    } dbpool;

    struct {
        int count;
        unsigned long *hosts;
    } trustedHosts;

    struct { /** guid.c **/
        XplSemaphore semaphore;
        unsigned char next[NMAP_GUID_LENGTH];
    } guid;
};

extern struct StoreGlobals StoreAgent;

#include "object-model.h"

/** store.c **/
int IsOwnStoreSelected(StoreClient *client);

int StoreCreateCollection(StoreClient *client, char *name, uint64_t guid,
                          uint64_t *outGuid, int32_t *outVersion);
int StoreCreateDocument(StoreClient *client, StoreDocumentType type, char *name, 
                        uint64_t *outGuid);
int SetupStore(const char *user, const char **storeRoot, char *path, size_t len);
CCode SelectUser(StoreClient *client, char *user, char *password, int nouser);
int SelectStore(StoreClient *client, char *user);
void UnselectUser(StoreClient *client);
void UnselectStore(StoreClient *client);
void DeleteStore(StoreClient *client);
void SetStoreReadonly(StoreClient *client, char const *reason);
void UnsetStoreReadonly(StoreClient *client);

CCode WriteDocumentHeaders(StoreClient *client, FILE *fh, const char *folder, time_t tod);

NMAPDeliveryCode DeliverMessageToMailbox(StoreClient *client,
                                         char *sender, char *authSender, 
                                         char *recipient, char *mbox,
                                         FILE *msgfile, unsigned long scmsID, 
                                         size_t msglen, unsigned long flags);

int ImportIncomingMail(StoreClient *client);

const char *StoreProcessDocument(StoreClient *client, 
                                 StoreObject *document,
                                 const char *path);

BongoJsonResult GetJson(StoreClient *client, StoreObject *object, BongoJsonNode **node, char *path);

/** watch.c **/
void StoreWatcherInit(void);
int StoreWatcherAdd(StoreClient *client, uint64_t collection);
int StoreWatcherRemove(StoreClient *client, uint64_t collection);
void StoreWatcherEvent(StoreClient *client, StoreObject *object, 
                       StoreWatchEvents event);

/** db.c **/

int     DBPoolInit(void);
int  StoreDBOpen(StoreClient *client, const char *user);
void StoreDBClose(StoreClient *client);

/** lock.c **/

CCode StoreShowWatcherEvents(StoreClient *client);


int StoreGetSharedLockQuiet(StoreClient *client, NLockStruct **lock,
                            uint64_t doc, time_t timeout);

int StoreGetExclusiveLockQuiet(StoreClient *client, NLockStruct **lock,
                               uint64_t doc, time_t timeout);

CCode StoreGetSharedLock(StoreClient *client, NLockStruct **lock,
                         uint64_t doc, time_t timeout);

CCode StoreGetExclusiveLock(StoreClient *client, NLockStruct **lock,
                            uint64_t doc, time_t timeout);

CCode StoreReleaseExclusiveLock(StoreClient *client, NLockStruct *lock);
CCode StoreReleaseSharedLock(StoreClient *client, NLockStruct *lock);

CCode StoreGetCollectionLock(StoreClient *client, NLockStruct **lock, uint64_t coll);
CCode StoreReleaseCollectionLock(StoreClient *client, NLockStruct **lock);


typedef struct {
    StoreClient *client;
    GArray *colls;
} CollectionLockPool;

int CollectionLockPoolInit(StoreClient *client, CollectionLockPool *pool);
void CollectionLockPoolDestroy(CollectionLockPool *pool);
NLockStruct *CollectionLockPoolGet(CollectionLockPool *pool, uint64_t coll);


typedef GArray WatchEventList;

int WatchEventListInit(WatchEventList *events);
void WatchEventListDestroy(WatchEventList *events);

int WatchEventListAdd(WatchEventList *events, 
                      NLockStruct *lock, StoreWatchEvents event,
                      uint64_t guid, uint32_t imapuid, uint32_t flags);

void WatchEventListFire(WatchEventList *events, StoreClient *client);


/** command.c **/
int StoreCommandLoop(StoreClient *client);
int StoreSetupCommands(void);

#include "command.h"

/** auth.c **/
#if 0
CCode StoreCheckAuthorization(StoreClient *client, DStoreDocInfo *info,
                              unsigned int neededPrivileges);

int StoreCheckAuthorizationQuiet(StoreClient *client, DStoreDocInfo *info,
                                 unsigned int neededPrivileges);

CCode StoreCheckAuthorizationGuid(StoreClient *client, uint64_t guid, 
                                  unsigned int neededPrivileges);

int StoreCheckAuthorizationGuidQuiet(StoreClient *client, uint64_t guid,
                                     unsigned int neededPrivileges);

CCode StoreCheckAuthorizationPath(StoreClient *client, const char *path,
                                  unsigned int neededPrivileges);
#endif

int StoreParseAccessControlList(StoreClient *client, const char *acl);

/** guid.c **/
void GuidReset(void);
void GuidAlloc(unsigned char *guid);
int NmapCommandGuid(void *param);

/** maildir.c **/
void FindPathToCollection(StoreClient *client, uint64_t collection, 
                          char *dest, size_t size);
void FindPathToDocument(StoreClient *client, uint64_t collection, 
                        uint64_t document, char *dest, size_t size);
void FindPathToObject(StoreClient *client, StoreObject *object, 
                      char *dest, size_t size);
int MaildirNew(const char *store_path, uint64_t collection);
int MaildirRemove(const char *store_path, uint64_t collection);
int MaildirTempDocument(StoreClient *client, uint64_t collection, char *dest, size_t size);
int MaildirDeliverTempDocument(StoreClient *client, uint64_t collection, const char *path, uint64_t uid);

/** mime.c **/
#include "mime.h"
int MimeGetInfo(StoreClient *client, StoreObject *object, MimeReport **outReport);
int MimeGetGuid(StoreClient *client, uint64_t guid, MimeReport **outReport);

/** config.c **/
BOOL StoreAgentReadConfiguration(BOOL *recover);

/** locking.c **/
typedef enum {
	LLOCK_NONE,
	LLOCK_READONLY,
	LLOCK_READWRITE
} LogicalLockType;

int LogicalLockGain(StoreClient *client, StoreObject *object, LogicalLockType type);
void LogicalLockRelease(StoreClient *client, StoreObject *object);

/* hardcoded guids: */

#define STORE_ROOT_GUID 1
#define STORE_ADDRESSBOOK_GUID 2
#define STORE_CALENDARS_GUID 3
#define STORE_ADDRESBOOK_GUID 4
#define STORE_MAIL_GUID 5
#define STORE_MAIL_INBOX_GUID 6
#define STORE_MAIL_DRAFTS_GUID 7
#define STORE_MAIL_ARCHIVES_GUID 8
#define STORE_ADDRESSBOOK_PERSONAL_GUID 9
#define STORE_ADDRESSBOOK_COLLECTED_GUID 10
#define STORE_PREFERENCES_GUID 11
#define STORE_CONVERSATIONS_GUID 12
#define STORE_EVENTS_GUID 13
#define STORE_DUMMY_INDEXER_GUID 14
#define STORE_CALENDARS_PERSONAL_GUID 15

#define STORE_CONVERSATIONS_COLLECTION "/conversations"

int _XplServiceMain(int argc, char *argv[]);

XPL_END_C_LINKAGE


#endif /* _STORED_H */
