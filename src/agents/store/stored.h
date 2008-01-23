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

#include <xpl.h>

XPL_BEGIN_C_LINKAGE

#include <bongoutil.h>
#include <connio.h>
#include <msgapi.h>
#include <bongoagent.h>
#include <nmap.h>
#include <bongostore.h>

#include <bongojson.h>
#include <bongocal.h>

#define LOGGERNAME "store"
#include <logger.h>

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

typedef struct _DStoreDocInfo {
    uint32_t flags;
    int32_t type;
    int64_t length;            // size of the document
    
    uint32_t timeCreatedUTC;   /* for imap */
    uint32_t timeModifiedUTC;  /* FIXME: remove this */

    int32_t version;
    int32_t indexed;           /* FIXME: remove this */

    uint32_t imapuid;     

    union {
        struct {
            char *subject;
            uint32_t numDocs;
            uint32_t numUnread;
            uint64_t lastMessageDate;
            uint32_t sources;
        } conversation;
        
        struct {
            uint64_t sent;
            uint32_t headerSize;
            char *subject;
            char *from;
            char *messageId;
            char *parentMessageId;
            char *listId;          /* mailing list */
            int32_t mimeLines;     /* 0 unknown, -1 none/error */
        } mail;

        struct {

            
        } calendar;

        struct {
            char start[DATETIME_LEN + 1];
            char end[DATETIME_LEN + 1];
            const char *uid;
            const char *summary;
            const char *location;
            const char *stamp;
        } event;

        struct {
            int needsCompaction;
        } collection;

        uint8_t padding[512];
    } data;
    
    /* keys go at bottom of struct.  guid must be the first key */
    uint64_t guid;
    uint64_t collection;
    uint64_t conversation;

    char filename[STORE_MAX_PATH];  /* for collection: path w/o trailing slash */
                                    /* for document:   full path '/' <docname> */
                                    /*        docnames may not contain slashes */
} DStoreDocInfo;


typedef enum {
    STORE_PROP_ALL,

    STORE_PROP_EXTERNAL,

    STORE_PROP_ACCESS_CONTROL,      /* an awkwardly-handled external property  */

    STORE_PROP_COLLECTION,
    STORE_PROP_CREATED,
    STORE_PROP_DOCUMENT,
    STORE_PROP_FLAGS,
    STORE_PROP_GUID,
    STORE_PROP_INDEX,
    STORE_PROP_LASTMODIFIED,
    STORE_PROP_LENGTH,
    STORE_PROP_TYPE,
    STORE_PROP_VERSION,

    STORE_PROP_EVENT_ALARM,         /* another external property */
    STORE_PROP_EVENT_CALENDARS,
    STORE_PROP_EVENT_END,
    STORE_PROP_EVENT_LOCATION,
    STORE_PROP_EVENT_START,
    STORE_PROP_EVENT_SUMMARY,
    STORE_PROP_EVENT_UID,
    STORE_PROP_EVENT_STAMP,

    STORE_PROP_MAIL_CONVERSATION,
    STORE_PROP_MAIL_HEADERLEN,
    STORE_PROP_MAIL_IMAPUID,
    STORE_PROP_MAIL_MESSAGEID,
    STORE_PROP_MAIL_PARENTMESSAGEID,
    STORE_PROP_MAIL_SENT,
    STORE_PROP_MAIL_SUBJECT,

    STORE_PROP_CONVERSATION_COUNT,
    STORE_PROP_CONVERSATION_DATE,
    STORE_PROP_CONVERSATION_SUBJECT,
    STORE_PROP_CONVERSATION_UNREAD,

} StorePropertyType;

#define STORE_IS_EXTERNAL_PROPERTY_TYPE(_proptype)                                   \
   (STORE_PROP_EXTERNAL == _proptype || STORE_PROP_ACCESS_CONTROL == _proptype ||    \
    STORE_PROP_EVENT_ALARM == _proptype)

typedef struct {
    StorePropertyType type;
    char *name;
    char *value;
    int valueLen;
} StorePropInfo;


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


#include "db.h"
#include "alarmdb.h"

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

struct _StoreClient {
    Connection *conn;
    DStoreHandle *handle;
    unsigned int flags;
    BongoMemStack memstack;
    int lockTimeoutMs;

    char buffer[CONN_BUFSIZE + 1];

    /* the following vars are initialized by the STORE command: */
    const char *storeRoot;
    unsigned long storeHash;         /* hash of store, should be uint32_t  */
    char store[XPL_MAX_PATH + 1];    /* "/storeroot/store/" */
    char *storeName;                 /* "store" */

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

    struct {
        AlarmDBHandle *alarmdb;
    } system;
};


typedef struct _DBPoolEntry DBPoolEntry;


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

        unsigned char host[MAXEMAILNAMESIZE + 1];
        unsigned char hash[NMAP_HASH_SIZE];
    } server;

    struct {
        unsigned char rootDir[XPL_MAX_PATH + 1];
        unsigned char spoolDir[XPL_MAX_PATH + 1];
        unsigned char systemDir[XPL_MAX_PATH + 1];
        /* FIXME: need a better name for this var: */
        int incomingQueueBytes;  /* max size of "incoming" file */ 
        int lockTimeoutMs;
    } store;

    struct {
        BongoHashtable *entries;
        DBPoolEntry *first;
        DBPoolEntry *last;
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


#include "index.h"

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

void FindPathToCollection(StoreClient *client, uint64_t collection, 
                       char *dest, size_t size);
void FindPathToDocument(StoreClient *client, uint64_t collection, 
                       uint64_t document, char *dest, size_t size);


CCode WriteDocumentHeaders(StoreClient *client, FILE *fh, const char *folder, time_t tod);

NMAPDeliveryCode DeliverMessageToMailbox(StoreClient *client,
                                         char *sender, char *authSender, 
                                         char *recipient, char *mbox,
                                         FILE *msgfile, unsigned long scmsID, 
                                         size_t msglen, unsigned long flags);

int ImportIncomingMail(StoreClient *client);

const char *StoreProcessDocument(StoreClient *client, 
                                 DStoreDocInfo *info,
                                 DStoreDocInfo *collection,
                                 char *path,
                                 IndexHandle *indexer,
                                 uint64_t linkGuid);

int SetParentCollectionIMAPUID(StoreClient *client, 
                               DStoreDocInfo *parent,
                               DStoreDocInfo *doc);

BongoJsonResult GetJson(StoreClient *client, DStoreDocInfo *doc, BongoJsonNode **node, char *path);

int StoreWatcherAdd(StoreClient *client, NLockStruct *cLock);
int StoreWatcherRemove(StoreClient *client, NLockStruct *cLock);
void StoreWatcherEvent(StoreClient *client, NLockStruct *cLock, 
                       StoreWatchEvents event,
                       uint64_t guid, uint32_t imapuid, uint32_t flags);

CCode StoreShowWatcherEvents(StoreClient *client);


int StoreGetSharedLockQuiet(StoreClient *client, NLockStruct **lock,
                            uint64_t doc, time_t timeout);

int StoreGetExclusiveLockQuiet(StoreClient *client, NLockStruct **lock,
                               uint64_t doc, time_t timeout);

CCode StoreGetSharedLock(StoreClient *client, NLockStruct **lock,
                         uint64_t doc, time_t timeout);

CCode StoreGetExclusiveLock(StoreClient *client, NLockStruct **lock,
                            uint64_t doc, time_t timeout);

#define StoreReleaseExclusiveLock(_clientptr, _lockptr) PurgeNLockRelease(_lockptr)
#define StoreReleaseSharedLock(_clientptr, _lockptr) ReadNLockRelease(_lockptr)

CCode StoreGetCollectionLock(StoreClient *client, NLockStruct **lock, uint64_t coll);


typedef struct {
    StoreClient *client;
    BongoArray colls;
} CollectionLockPool;

int CollectionLockPoolInit(StoreClient *client, CollectionLockPool *pool);
void CollectionLockPoolDestroy(CollectionLockPool *pool);
NLockStruct *CollectionLockPoolGet(CollectionLockPool *pool, uint64_t coll);


typedef BongoArray WatchEventList;

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

int StoreParseAccessControlList(StoreClient *client, const char *acl);

/** guid.c **/
void GuidReset(void);
void GuidAlloc(unsigned char *guid);
int NmapCommandGuid(void *param);

/** mime.c **/

int MimeGetInfo(StoreClient *client, DStoreDocInfo *info, MimeReport **outReport);
int MimeGetGuid(StoreClient *client, uint64_t guid, MimeReport **outReport);

/** config.c **/
BOOL StoreAgentReadConfiguration(BOOL *recover);

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
