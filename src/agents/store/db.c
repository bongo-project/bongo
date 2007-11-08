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
#include <config.h>
#include <xpl.h>
#include <sqlite3.h>
#include <assert.h>


#include "stored.h"


/****************************************************

SQLITE DATABASE SCHEMA:

docs table:
   guid        - the collection's or document's guid 64 bit primary key
   filename    - for collection: path w/o trailing slash
                 for document:   full path + '/' + document name (see stored.h)
   collection  - the guid of the containing collection

   info        - contains DStoreDocInfo struct

   subject     - for DOCTYPE_MAIL and DOCTYPE_CONVERSATION
   senders     - for DOCTYPE_MAIL
   msgid       - for DOCTYPE_MAIL
   parentmsgid - for DOCTYPE_MAIL
   listid      - for DOCTYPE_MAIL

   uid         - for DOCTYPE_EVENT
   summary     - for DOCTYPE_EVENT
   location    - for DOCTYPE_EVENT
   stamp       - for DOCTYPE_EVENT

props table:
   guid        - guid of doc primary key
   name        - name of property  (utf-8)  may not contain spaces or commas
   value       - value of property (utf-8)

conversations table:
   guid        - key to row in docs table
   subject     - normalized subject
   date        - 
   sources     - bitfield in stored.h

events table:
   guid        -
   start
   end
   flags
   summary
   location

links table:
   calendar
   event

*****************************************************/


struct _DStoreStmt {
    sqlite3_stmt *stmt;
    InfoStmtFilter *filter;
    void *userdata;
};

struct _DStoreHandle {
    sqlite3 *db;

    struct { 
        /* NOTE: begin and end must be the first and last stmts (see DStoreClose()) */
        DStoreStmt begin;       /* begin exclusive transaction */
        DStoreStmt beginShared; /* begin shared transaction */

        DStoreStmt getGuidDoc;
        DStoreStmt getInfo;
        DStoreStmt getInfoGuid;
        DStoreStmt getInfoCollection;
        DStoreStmt getInfoFilename;
        DStoreStmt getColls;
        DStoreStmt addInfo;
        DStoreStmt setInfo;
        DStoreStmt setFilename;
        DStoreStmt delInfo;
        DStoreStmt delColl;
        DStoreStmt getGuidColl;
        DStoreStmt listColl;
        DStoreStmt listCollRange;
        DStoreStmt listUnindexed;
        DStoreStmt listUnindexedColl;
        DStoreStmt setIndexed;
        DStoreStmt renameDocs;

        DStoreStmt delTempDocsTable;
        DStoreStmt addTempInfo;
        DStoreStmt mergeTempDocsTable;

        DStoreStmt getProps;
        DStoreStmt getPropName;
        DStoreStmt setProp;

        DStoreStmt listConversations;
        DStoreStmt listConversationsRange;
        DStoreStmt listConversationsCentered;
        DStoreStmt countConversations;
        DStoreStmt addConversation;
        DStoreStmt updateConversation;
        DStoreStmt findConversation;
        DStoreStmt findConversationMembers;
        DStoreStmt findConversationSenders;
        DStoreStmt findMailingLists;

        DStoreStmt countMessages;
        DStoreStmt findMessages;
        DStoreStmt findMessagesCentered;
        DStoreStmt findMessagesRange;
        DStoreStmt findMessagesRecip;
        DStoreStmt findMessagesRecipRange;
            

        DStoreStmt guidList;

        DStoreStmt setEvent;
        DStoreStmt delEvent;
        DStoreStmt findEvents;
        DStoreStmt findEventsCal;
        DStoreStmt findEventsUid;
        DStoreStmt findCalsEvent;
        DStoreStmt setLink;
        DStoreStmt delLink;
        DStoreStmt delUnlinkedEvent;
        DStoreStmt delUnlinkedDoc;
        DStoreStmt delCalendarLink;

        DStoreStmt delOrphanedEventDocs;
        DStoreStmt delOrphanedEvents;
        
        DStoreStmt getMime;
        DStoreStmt setMime;
        DStoreStmt clearMime;

        DStoreStmt listJournal;
        DStoreStmt addJournalEntry;
        DStoreStmt delJournalEntry;

        DStoreStmt getVersion;

        DStoreStmt abrt;        /* rollback transaction */
        DStoreStmt end;         /* end transaction */
    } stmts;

    struct {
        const char *setInfoFilename;
        const char *maskFilter;
        const char *headerInfo;
    } funcs;

    struct {                       /* userdata for test_headers() */
        StoreHeaderInfo *headers;
        int count;
    } headersdata;

    struct {                       /* userdata fro test_guid() */
        uint64_t *guids;
    } guidsdata;

    int transactionDepth;          /* 0 or 1 since sqlite3 transactions don't nest */
    int lockTimeoutMs;
    
    BongoMemStack *memstack;
};

#define STMT_SLEEP_MS 250


static DStoreStmt *SqlPrepare(DStoreHandle *handle, const char *sql, DStoreStmt *stmt);


static void
DStoreReset(DStoreHandle *handle)
{
    DStoreStmt *stmt;

    for (stmt = &handle->stmts.begin; 
         stmt <= &handle->stmts.end; 
         stmt++)
    {
        if (stmt->stmt) {
            sqlite3_finalize(stmt->stmt);
            stmt->stmt = NULL;
        }
    }
}


/* returns 0 on success */
static int
DStoreCreateDB(DStoreHandle *handle)
{
    int dcode;
    
    if (DStoreBeginTransaction(handle)) {
        goto fail;
    }

    dcode = 
        sqlite3_exec (handle->db, 
                      "PRAGMA user_version=2;"

                      "CREATE TABLE docs (guid INTEGER PRIMARY KEY AUTOINCREMENT, "
                      "                   conversation INTEGER, "
                      "                   filename TEXT DEFAULT NULL, "
                      "                   collection INTEGER DEFAULT 0, "

                      "                   info BLOB NOT NULL,"
                      "                   subject TEXT DEFAULT NULL,"
                      "                   senders TEXT DEFAULT NULL,"
                      "                   msgid TEXT DEFAULT NULL,"
                      "                   parentmsgid TEXT DEFAULT NULL,"
                      "                   listid TEXT DEFAULT NULL, "
                      "                   uid TEXT DEFAULT NULL,"
                      "                   summary TEXT DEFAULT NULL,"
                      "                   location TEXT DEFAULT NULL,"
                      "                   stamp TEXT DEFAULT NULL"
                      ");"
                      "CREATE UNIQUE INDEX filename_idx ON docs (filename);"
                      "CREATE INDEX docs_idx ON docs (guid);"
 
                      "CREATE TABLE props (guid INTEGER, "
                      "                    name TEXT NOT NULL, "
                      "                    value TEXT NOT NULL);"
                      "CREATE UNIQUE INDEX props_index ON props (guid, name);"

                      "CREATE TABLE conversations (guid INTEGER, "
                      "                            subject TEXT NOT NULL, "
                      "                            date INTEGER, "
                      "                            sources INTEGER);"
                      "CREATE INDEX conv_subject_idx ON conversations(subject);"
                      "CREATE INDEX conv_guid_idx ON conversations(guid);"
                      "CREATE INDEX conv_date_idx ON conversations(date);"

                      "CREATE TABLE events (guid INTEGER PRIMARY KEY, "
                      "                     uid TEXT, "
                      "                     start INTEGER, "
                      "                     end INTEGER, "
                      "                     flags INTEGER);"

                      "CREATE TABLE links (calendar INTEGER, "
                      "                    event INTEGER);"
                      "CREATE UNIQUE INDEX links_index ON links (calendar, event);"

                      "CREATE TABLE tempdocs (guid INTEGER PRIMARY KEY, "
                      "                       info BLOB NOT NULL);"

                      "CREATE TABLE journal (collection INTEGER PRIMARY KEY, "
                      "                      filename TEXT DEFAULT NULL);"

                      "CREATE TABLE mime_cache (guid INTEGER PRIMARY KEY, "
                      "                         value BLOB NOT NULL);"
                      ,
                      NULL, NULL, NULL);
    
    if (SQLITE_OK != dcode) {
        goto fail;
    }

    /* schema has changed; clear prepared stmts */
    DStoreReset(handle);

    /* create default collections */
    { 
        DStoreDocInfo info;

        memset (&info, 0, sizeof(DStoreDocInfo));

        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_ROOT_GUID;
        info.filename[0] = '/';
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }
        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_ADDRESSBOOK_GUID;
        info.collection = STORE_ROOT_GUID;
        strncpy(info.filename, "/addressbook", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        } 
        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_CONVERSATIONS_GUID;
        info.collection = STORE_ROOT_GUID;
        strncpy(info.filename, STORE_CONVERSATIONS_COLLECTION, sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }
        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_CALENDARS_GUID;
        info.collection = STORE_ROOT_GUID;
        strncpy(info.filename, "/calendars", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }
        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_MAIL_GUID;
        info.collection = STORE_ROOT_GUID;
        strncpy(info.filename, "/mail", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }
        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_MAIL_INBOX_GUID;
        info.collection = STORE_MAIL_GUID;
        strncpy(info.filename, "/mail/INBOX", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }

        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_MAIL_DRAFTS_GUID;
        info.collection = STORE_MAIL_GUID;
        strncpy(info.filename, "/mail/drafts", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }

        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_MAIL_ARCHIVES_GUID;
        info.collection = STORE_MAIL_GUID;
        strncpy(info.filename, "/mail/archives", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }

        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_ADDRESSBOOK_PERSONAL_GUID;
        info.collection = STORE_ADDRESSBOOK_GUID;
        strncpy(info.filename, "/addressbook/personal", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }
        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_ADDRESSBOOK_COLLECTED_GUID;
        info.collection = STORE_ADDRESSBOOK_GUID;
        strncpy(info.filename, "/addressbook/collected", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }
        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_PREFERENCES_GUID;
        info.collection = STORE_ROOT_GUID;
        strncpy(info.filename, "/preferences", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }
        info.type = STORE_DOCTYPE_FOLDER;
        info.guid = STORE_EVENTS_GUID;
        info.collection = STORE_ROOT_GUID;
        strncpy(info.filename, "/events", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }

        /* FIXME: temporarily abusing the guid locks for the indexer */
        info.type = STORE_DOCTYPE_UNKNOWN;
        info.guid = STORE_DUMMY_INDEXER_GUID;
        info.collection = 0;
        strncpy(info.filename, "/-", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }

        info.type = STORE_DOCTYPE_CALENDAR;
        info.guid = STORE_CALENDARS_PERSONAL_GUID;
        info.collection = STORE_CALENDARS_GUID;
        strncpy(info.filename, "/calendars/Personal", sizeof(info.filename) - 1);
        if (DStoreSetDocInfo(handle, &info)) {
            goto fail;
        }
    }
    
    if (DStoreCommitTransaction(handle)) {
        goto fail;
    }

    return 0;

fail:
    Log(LOG_ERROR, "DStoreCreateDB error: %s", sqlite3_errmsg(handle->db));

    DStoreAbortTransaction(handle);
    DStoreClose(handle);
    return -1;
}


/* ensure the database schema has been updated to the proper version 
   returns: 0 on success, -1 on error
*/

static int
UpgradeDB(DStoreHandle *handle)
{
    int dcode;
    DStoreStmt *stmt;
    int version = -1;
    char buf[32];

#define CURRENT_VERSION 2

    if (DStoreBeginTransaction(handle)) {
        Log(LOG_ERROR, "UpgradeDB error: %s", sqlite3_errmsg(handle->db));
        return -1;
    }

    stmt = SqlPrepare(handle, "PRAGMA user_version;", &handle->stmts.getVersion);
    if (!stmt) {
        Log(LOG_ERROR, "UpgradeDB error: %s", sqlite3_errmsg(handle->db));
        goto fail;
    }
    
    if (1 != DStoreStmtStep(handle, stmt)) {
        Log(LOG_ERROR, "UpgradeDB error: %s", sqlite3_errmsg(handle->db));
        DStoreStmtEnd(handle, stmt);
        goto fail;
    }
    version = sqlite3_column_int(stmt->stmt, 0);
    DStoreStmtEnd(handle, stmt);

    if (CURRENT_VERSION == version) {
        DStoreAbortTransaction(handle);
        return 0;
    }

    switch (version) {
    case 0:
        dcode = sqlite3_exec(handle->db,
                             "CREATE TABLE mime_cache (guid INTEGER PRIMARY KEY, "
                             "                         value BLOB NOT NULL);"
                             ,
                             NULL, NULL, NULL);
        if (SQLITE_OK != dcode) {
            Log(LOG_ERROR, "Couldn't add mime_cache table: %s\n", sqlite3_errmsg(handle->db));
            goto fail;
        }        
    case 1:
        dcode = sqlite3_exec(handle->db,
                             "ALTER TABLE docs ADD COLUMN stamp TEXT DEFAULT NULL;",
                             NULL, NULL, NULL);
        if (SQLITE_OK != dcode) {
            Log(LOG_ERROR, "Couldn't update docs table: %s\n", sqlite3_errmsg(handle->db));
            goto fail;
        }
    case 2:
        break;
    default:
        Log(LOG_ERROR, "Illegal database version %d found", CURRENT_VERSION);
        goto fail;
    }

    snprintf(buf, sizeof(buf), "PRAGMA user_version=%d;", CURRENT_VERSION);

    dcode = sqlite3_exec(handle->db, buf, NULL, NULL, NULL);
    if (SQLITE_OK != dcode || DStoreCommitTransaction(handle)) {
        goto fail;
    }

    DStoreReset(handle);

    Log(LOG_INFO, "Upgraded store to version %d", CURRENT_VERSION);
    return 0;

fail:
    DStoreAbortTransaction(handle);
    if (-1 != version) {
        Log(LOG_ERROR, "Unable to upgrade store from version %d to version %d.", 
               version, CURRENT_VERSION);
    }
    return -1;
}


static void test_info_flags(sqlite3_context *ctx, int argc, sqlite3_value **argv);

static void test_headers(sqlite3_context *ctx, int argc, sqlite3_value **argv);

static void test_guid(sqlite3_context *ctx, int argc, sqlite3_value **argv);

DStoreHandle *
DStoreOpen(char *basepath, BongoMemStack *memstack, int locktimeoutms)
{
    char path[XPL_MAX_PATH + 1];
    DStoreHandle *handle = NULL;
    int create = 0;

    snprintf(path, sizeof(path), "%sstore.db", basepath);
    path[sizeof(path)-1] = 0;

    create = access(path, 0);

    if (!(handle = MemMalloc(sizeof(struct _DStoreHandle)))   ||
         (memset(handle, 0, sizeof(struct _DStoreHandle)), 0) ||
         (SQLITE_OK != sqlite3_open(path, &handle->db)))
    {
        Log(LOG_ERROR, "Failed to open dstore \"%s\"", path);
        goto fail;
    }

    handle->memstack = memstack;

    handle->lockTimeoutMs = locktimeoutms;

    if (create && DStoreCreateDB(handle)) {
        Log(LOG_ERROR, "Couldn't open dstore db");
        goto fail;
    }

    if (UpgradeDB(handle)) {
        goto fail;
    }

    if (sqlite3_create_function(handle->db, "test_info_flags", 3, 
                                SQLITE_UTF8, NULL, 
                                test_info_flags, NULL, NULL) ||
        sqlite3_create_function(handle->db, "test_headers", 4, 
                                SQLITE_UTF8, handle, 
                                test_headers, NULL, NULL) ||
        sqlite3_create_function(handle->db, "test_guid", 1, 
                                SQLITE_UTF8, handle, 
                                test_guid, NULL, NULL) )
    {
        goto fail;
    }

    return handle;

fail:
    if (handle) {
        if (handle->db) {
            sqlite3_close(handle->db);
        }

        MemFree(handle);
    }
    return NULL;
}


void
DStoreClose(DStoreHandle *handle)
{
    DStoreReset(handle);
    if (SQLITE_BUSY == sqlite3_close(handle->db)) {
        Log (LOG_ERROR, "Couldn't close dstore database");
    }

    MemFree(handle);
}


BongoMemStack *
DStoreGetMemStack(DStoreHandle *handle)
{
    return handle->memstack;
}


void 
DStoreSetMemStack(DStoreHandle *handle, BongoMemStack *memstack)
{
    handle->memstack = memstack;
}


void
DStoreSetLockTimeout(DStoreHandle *handle, int timeoutms)
{
    handle->lockTimeoutMs = timeoutms;
}


static DStoreStmt *
SqlPrepare(DStoreHandle *handle, const char *statement, DStoreStmt *stmt)
{
    int count = 1 + handle->lockTimeoutMs / STMT_SLEEP_MS;

    stmt->filter = NULL;
    if (stmt->stmt) {
        return stmt;
    }

    while (count--) {
        switch (sqlite3_prepare(handle->db, statement, -1, &stmt->stmt, NULL)) {
        case SQLITE_OK:
            return stmt;
        case SQLITE_BUSY:
            XplDelay(STMT_SLEEP_MS);
            continue;
        default:
            Log(LOG_INFO, "SQL Prepare statement \"%s\" failed; %s", 
                             statement, sqlite3_errmsg(handle->db));
            return NULL;
        }
    }
    Log(LOG_ERROR, "Sql Prepare failed because db is busy.\r\n");
    return NULL;
}


static int
DStoreBindStr(sqlite3_stmt *stmt, int var, const char *str)
{
    if (str) {
        return sqlite3_bind_text(stmt, var, str, strlen(str), SQLITE_STATIC);
    } else {
        return sqlite3_bind_text(stmt, var, "", 0, SQLITE_STATIC);
    }
}


static int
DStoreBindStrOrNull(sqlite3_stmt *stmt, int var, const char *str)
{
    if (str) {
        return sqlite3_bind_text(stmt, var, str, strlen(str), SQLITE_STATIC);
    } else {
        return sqlite3_bind_null(stmt, var);
    }
}


#define DOCINFO_SIZE (sizeof(DStoreDocInfo))


void
DStoreInfoStmtAddFilter(DStoreStmt *stmt, InfoStmtFilter *filter, void *userdata)
{
    stmt->filter = filter;
    stmt->userdata = userdata;
}


/* returns: 1 if row available
            0 if no row available,
            -1 on error
*/


int
DStoreStmtStep(DStoreHandle *handle, 
               DStoreStmt *_stmt)
{
    sqlite3_stmt *stmt = _stmt->stmt;
    int count = 1 + handle->lockTimeoutMs / STMT_SLEEP_MS;
    int dbg;
    while (count--) {
        switch ((dbg = sqlite3_step(stmt))) {
        case SQLITE_ROW:
            return 1;
        case SQLITE_DONE:
            return 0;
        case SQLITE_BUSY:
            XplDelay(STMT_SLEEP_MS);
            continue;
        default:
            Log(LOG_INFO, "Sql: %s", sqlite3_errmsg(handle->db));
            return -1;
        }
    }
    Log(LOG_ERROR, "Sql Busy.");
    return -1;
}


int
DStoreStmtExecute(DStoreHandle *handle, DStoreStmt *_stmt)
{
    sqlite3_stmt *stmt = _stmt->stmt;
    int result;

    result = sqlite3_step(stmt);
    if (SQLITE_DONE == result) {
        return 0;
    } else {
        Log(LOG_ERROR, "Sql: %s", sqlite3_errmsg(handle->db));
        return -1;
    }
}


void
DStoreStmtError(DStoreHandle *handle, DStoreStmt *stmt)
{
    Log(LOG_ERROR, "Sql: %s\r\n", sqlite3_errmsg(handle->db));
}


void
DStoreStmtEnd(DStoreHandle *handle, DStoreStmt *stmt)
{
    sqlite3_reset(stmt->stmt);
}


/* returns 0 on success, -2 db busy, -1 on error */

int
DStoreBeginTransaction(DStoreHandle *handle)
{
    DStoreStmt *stmt;
    int result;
    int count = 1 + handle->lockTimeoutMs / STMT_SLEEP_MS;

    if (handle->transactionDepth) {
        /* FIXME: ensure it's an exclusive transaction */
        abort ();
        return 0;
    }

    stmt = SqlPrepare(handle, "BEGIN EXCLUSIVE TRANSACTION;", &handle->stmts.begin);
    if (!stmt) {
        DStoreStmtError(handle, stmt);
        return -1;
    }

    do {
        result = sqlite3_step(stmt->stmt);
        sqlite3_reset(stmt->stmt);
    } while (SQLITE_BUSY == result && --count && (XplDelay(STMT_SLEEP_MS), 1));

    switch (result) {
    case SQLITE_DONE:
        ++handle->transactionDepth;
        return 0;
    case SQLITE_BUSY:
        return -2;
    default:
        DStoreStmtError(handle, stmt);
        return -1;
    }
}


/* returns 0 on success, -2 db busy, -1 on error */

int
DStoreBeginSharedTransaction(DStoreHandle *handle)
{
    DStoreStmt *stmt;
    int result;
    int count = 1 + handle->lockTimeoutMs / STMT_SLEEP_MS;

    if (handle->transactionDepth) {
        ++handle->transactionDepth;
        return 0;
    }

    stmt = SqlPrepare(handle, "BEGIN TRANSACTION;", &handle->stmts.beginShared);
    if (!stmt) {
        DStoreStmtError(handle, stmt);
        return -1;
    }

    /* sqlite3 doesn't actually try to obtain a lock until the first statement
       is executed, so this check for SQLITE_BUSY is probably unneeded... */
    do {
        result = sqlite3_step(stmt->stmt);
        sqlite3_reset(stmt->stmt);
    } while (SQLITE_BUSY == result && --count && (XplDelay(STMT_SLEEP_MS), 1));

    switch (result) {
    case SQLITE_DONE:
        ++handle->transactionDepth;
        return 0;
    case SQLITE_BUSY:
        return -2;
    default:
        return -1;
    }
}


/* returns 0 on success, -1 on error */
int
DStoreCommitTransaction(DStoreHandle *handle)
{
    DStoreStmt *stmt;
    int result;
    int count = 1 + handle->lockTimeoutMs / STMT_SLEEP_MS;

    stmt = SqlPrepare(handle, "END TRANSACTION;", &handle->stmts.end);
    if (!stmt) {
        DStoreStmtError(handle, stmt);
        return -1;
    }

    do {
        result = sqlite3_step(stmt->stmt);
        sqlite3_reset(stmt->stmt);
    } while (SQLITE_BUSY == result && --count && (XplDelay(STMT_SLEEP_MS), 1));

    --handle->transactionDepth;

    if (SQLITE_DONE != result) {
        DStoreStmtError(handle, stmt);
        return -1;
    }
    return 0;
}


/* returns 0 on success, -1 on error */
int
DStoreAbortTransaction(DStoreHandle *handle)
{
    DStoreStmt *stmt;
    int result;

    stmt = SqlPrepare(handle, "ROLLBACK TRANSACTION;", &handle->stmts.abrt);
    if (!stmt) {
        DStoreStmtError(handle, stmt);
        return -1;
    }

    result = sqlite3_step(stmt->stmt);
    if (SQLITE_DONE != result) {
        DStoreStmtError(handle, stmt);
    }
    sqlite3_reset(stmt->stmt);
    --handle->transactionDepth;
    return SQLITE_DONE == result ? 0 : -1;
}


int
DStoreCancelTransactions(DStoreHandle *handle)
{
    while (handle->transactionDepth) {
        if (DStoreAbortTransaction(handle)) {
            return -1;
        }
     }
    return 0;
}

        /****************************************************************/


#define DOCINFO_COLS " docs.guid, docs.info, " \
                     " docs.subject, docs.senders, docs.msgid, docs.parentmsgid, " \
                     " docs.conversation, docs.listid," \
                     " docs.uid, docs.summary, docs.location, docs.stamp "
#define CONVINFO_COLS " docs.guid, docs.info, conversations.subject "
#define CONVINFO_COLSAS " docs.guid as guid, docs.info as info, conversations.subject as subject"
#define EVENTINFO_COLS " events.guid, docs.info, " \
                       " 0, 0, 0, 0, 0, 0, " \
                       " docs.uid, docs.summary, docs.location, docs.stamp "

/* returns: 1 on success
            0 if no row available,
            -1 on error
*/

int
DStoreInfoStmtStep(DStoreHandle *handle, 
                   DStoreStmt *stmt,
                   DStoreDocInfo *info)
{
    int result;
    const void *data;
    void *mark = BongoMemStackPeek(handle->memstack);

retry:

    if (1 != (result = DStoreStmtStep(handle, stmt))) {
        return result;
    }
    if (DOCINFO_SIZE != sqlite3_column_bytes(stmt->stmt, 1)) {
        Log(LOG_ERROR, "Store DB error: Incompatible docs table.");
        return -1;
    }
    data = sqlite3_column_blob(stmt->stmt, 1);
    if (!data) {
        return -1;
    }
    memcpy (info, data, DOCINFO_SIZE);
    info->guid = sqlite3_column_int64(stmt->stmt, 0);

#define GET_COL(colno, var)                                                 \
{                                                                           \
    int len = sqlite3_column_bytes(stmt->stmt, colno);                      \
    if (!len) {                                                             \
        var = NULL;                                                         \
    } else {                                                                \
        var = BongoMemStackPushStr(handle->memstack,                         \
                                  sqlite3_column_text(stmt->stmt, colno));  \
        if (!var) {                                                         \
            return -1;                                                      \
        }                                                                   \
    }                                                                       \
}
    
    switch (info->type) {
    case STORE_DOCTYPE_CONVERSATION:
        GET_COL(2, info->data.conversation.subject);
        break;
    case STORE_DOCTYPE_MAIL:
        GET_COL(2, info->data.mail.subject);
        GET_COL(3, info->data.mail.from);
        GET_COL(4, info->data.mail.messageId);
        GET_COL(5, info->data.mail.parentMessageId);
        info->conversation = sqlite3_column_int64(stmt->stmt, 6);
        GET_COL(7, info->data.mail.listId);
        break;
    case STORE_DOCTYPE_EVENT:
        GET_COL(8, info->data.event.uid);
        GET_COL(9, info->data.event.summary);
        GET_COL(10, info->data.event.location);
        GET_COL(11, info->data.event.stamp);

    default:
        break;
    }

#undef GET_COL

    if (stmt->filter && !stmt->filter(info, stmt->userdata)) {
        BongoMemStackPop(handle->memstack, mark);
        goto retry;
    }

    return 1;
}


/* caller must provide one of guid, collection, or filename.  If none are specified,
   all docs are returned */

DStoreStmt *
DStoreFindDocInfo(DStoreHandle *handle,
                  uint64_t guid,
                  char *coll,
                  char *filename)
{
    DStoreStmt *stmt;

    if (guid) {
        stmt = SqlPrepare(handle, "SELECT" DOCINFO_COLS "FROM docs WHERE guid = ?;",
                          &handle->stmts.getInfoGuid);
        if (!stmt || sqlite3_bind_int64(stmt->stmt, 1, guid)) {
            DStoreStmtError(handle, stmt);
            return NULL;
        }
    } else if (coll) {
        stmt = SqlPrepare(handle, 
                          "SELECT" DOCINFO_COLS "FROM docs WHERE collection = ?;",
                          &handle->stmts.getInfoCollection);
        if (!stmt || DStoreBindStr(stmt->stmt, 1, coll)) {
            return NULL;
        }
    } else if (filename) {
        stmt = SqlPrepare(handle, 
                          "SELECT" DOCINFO_COLS "FROM docs WHERE filename = ?;",
                          &handle->stmts.getInfoFilename);
       if (!stmt || DStoreBindStr(stmt->stmt, 1, filename)) {
           return NULL;
       }
    } else {
        return SqlPrepare(handle, "SELECT" DOCINFO_COLS "FROM docs;",
                          &handle->stmts.getInfo);
    }
    return stmt;
}


/* returns: -1 sql error, 0 no records found, 1 one record*/

int
DStoreGetDocInfoGuid(DStoreHandle *handle, uint64_t guid, DStoreDocInfo *result)
{
    DStoreStmt *stmt;
    int dcode = -1;

    if (!guid) {
        return 0;
    }

    stmt = DStoreFindDocInfo(handle, guid, NULL, NULL);
    if (!stmt) {
        return -1;
    }
    dcode = DStoreInfoStmtStep(handle, stmt, result);
    DStoreStmtEnd(handle, stmt);
    return dcode;
}


int
DStoreGetDocInfoFilename(DStoreHandle *handle, char *filename, DStoreDocInfo *result)
{
    DStoreStmt *stmt;
    int dcode = -1;

    stmt = DStoreFindDocInfo(handle, 0, NULL, filename);
    if (!stmt) {
        return -1;
    }
    dcode = DStoreInfoStmtStep(handle, stmt, result);
    DStoreStmtEnd(handle, stmt);
    return dcode;
}
    

static int
SetConversation(DStoreHandle *handle, BOOL newDoc, DStoreDocInfo *info)
{
    DStoreStmt *stmt;
    
    if (newDoc) {
        stmt = SqlPrepare(handle,
                          "INSERT into conversations (guid, subject, date, sources) "
                          "VALUES(?, ?, ?, ?);",
                          &handle->stmts.addConversation);
        if (!stmt ||
            sqlite3_bind_int64(stmt->stmt, 1, info->guid) ||
            DStoreBindStrOrNull(stmt->stmt, 2, info->data.conversation.subject) ||
            sqlite3_bind_int64(stmt->stmt, 3, 
                               info->data.conversation.lastMessageDate) ||
            sqlite3_bind_int(stmt->stmt, 4, info->data.conversation.sources)) {
            return -1;
        }
    } else {
        stmt = SqlPrepare(handle,
                          "UPDATE conversations SET date=?, sources=? WHERE guid=?;",
                          &handle->stmts.updateConversation);
        if (!stmt ||
            sqlite3_bind_int64(stmt->stmt, 1, 
                               info->data.conversation.lastMessageDate) ||
            sqlite3_bind_int64(stmt->stmt, 2, info->data.conversation.sources) ||
            sqlite3_bind_int64(stmt->stmt, 3, info->guid)) {
            return -1;
        }
    }

    if (DStoreStmtExecute(handle, stmt)) {
        return -1;
    }
    
    DStoreStmtEnd(handle, stmt);
    
    return 0;
}



/* inserts or updates a new docinfo record.

   info.guid -       if set to 0, a new doc is inserted.  info.guid will be set 
                     to the newly allocated guid.

   info.filename -   "/foo/bar/baz", "/", "X" - used directly.
                     "/foo/bar/baz/" - a new filename will be generated from the guid.

*/

int
DStoreSetDocInfo(DStoreHandle *handle, DStoreDocInfo *info)
{
    DStoreStmt *stmt;
    int dcode = 0;
    BOOL newDoc = FALSE;
    char *filename;

    info->timeModifiedUTC = time(NULL);

    assert('/' == info->filename[0] || 'X' == info->filename[0]);
    
    if (info->filename[1] && !strrchr(info->filename, '/')[1]) {
        filename = "";
    } else {
        filename = info->filename;
    }

    if (info->guid) {
        stmt = SqlPrepare(handle, 
                          "REPLACE into docs "
                          "(guid, filename, collection, info, "
                          " conversation, "
                          " subject, senders, msgid, parentmsgid, listid, "
                          " uid, summary, location, stamp) "
                          "VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, "
                          "        ?11, ?12, ?13, ?14);", 
                          &handle->stmts.setInfo);
    } else {
        newDoc = TRUE;
        stmt = SqlPrepare(handle, 
                          "INSERT into docs "
                          "(filename, collection, info, "
                          " conversation, "
                          " subject, senders, msgid, parentmsgid, listid, "
                          " uid, summary, location, stamp) "
                          "VALUES (?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, "
                          "        ?11, ?12, ?13, ?14);", 
                          &handle->stmts.addInfo);
    }
    if (!stmt) {
        return -1;
    }
    if ((info->guid && sqlite3_bind_int64(stmt->stmt, 1, info->guid)) ||
        DStoreBindStr(stmt->stmt, 2, filename) ||
        sqlite3_bind_int64(stmt->stmt, 3, info->collection) ||
        sqlite3_bind_blob(stmt->stmt, 4, info, DOCINFO_SIZE, SQLITE_STATIC) ||
        sqlite3_bind_int64(stmt->stmt, 5, info->conversation))
    {
        dcode = -1;
    }

    switch (info->type) {
    case STORE_DOCTYPE_CONVERSATION:
        dcode = dcode || 
            DStoreBindStrOrNull(stmt->stmt, 6, info->data.conversation.subject) ||
            sqlite3_bind_null(stmt->stmt, 7) ||
            sqlite3_bind_null(stmt->stmt, 8) ||
            sqlite3_bind_null(stmt->stmt, 9) ||
            sqlite3_bind_null(stmt->stmt, 10) ||
            sqlite3_bind_null(stmt->stmt, 11) ||
            sqlite3_bind_null(stmt->stmt, 12) ||
            sqlite3_bind_null(stmt->stmt, 13) ||
            sqlite3_bind_null(stmt->stmt, 14);
        break;
    case STORE_DOCTYPE_MAIL:
        dcode = dcode ||
            DStoreBindStrOrNull(stmt->stmt, 6, info->data.mail.subject) ||
            DStoreBindStrOrNull(stmt->stmt, 7, info->data.mail.from) ||
            DStoreBindStrOrNull(stmt->stmt, 8, info->data.mail.messageId) ||
            DStoreBindStrOrNull(stmt->stmt, 9, info->data.mail.parentMessageId) ||
            DStoreBindStrOrNull(stmt->stmt, 10, info->data.mail.listId) ||
            sqlite3_bind_null(stmt->stmt, 11) ||
            sqlite3_bind_null(stmt->stmt, 12) ||
            sqlite3_bind_null(stmt->stmt, 13) ||
            sqlite3_bind_null(stmt->stmt, 14);
        break;
    case STORE_DOCTYPE_EVENT:
        dcode = dcode || 
            sqlite3_bind_null(stmt->stmt, 6) ||
            sqlite3_bind_null(stmt->stmt, 7) ||
            sqlite3_bind_null(stmt->stmt, 8) ||
            sqlite3_bind_null(stmt->stmt, 9) ||
            sqlite3_bind_null(stmt->stmt, 10) ||
            DStoreBindStrOrNull(stmt->stmt, 11, info->data.event.uid) ||
            DStoreBindStrOrNull(stmt->stmt, 12, info->data.event.summary) ||
            DStoreBindStrOrNull(stmt->stmt, 13, info->data.event.location) ||
            DStoreBindStrOrNull(stmt->stmt, 14, info->data.event.stamp);
        break;
    default:
        dcode = dcode || 
            sqlite3_bind_null(stmt->stmt, 6) ||
            sqlite3_bind_null(stmt->stmt, 7) ||
            sqlite3_bind_null(stmt->stmt, 8) ||
            sqlite3_bind_null(stmt->stmt, 9) ||
            sqlite3_bind_null(stmt->stmt, 10) ||
            sqlite3_bind_null(stmt->stmt, 11) ||
            sqlite3_bind_null(stmt->stmt, 12) ||
            sqlite3_bind_null(stmt->stmt, 13) ||
            sqlite3_bind_null(stmt->stmt, 14);
        break;
    }
    if (dcode || DStoreStmtExecute(handle, stmt)) {
        dcode = -1;
    }

    DStoreStmtEnd(handle, stmt);

    if (!info->guid) { 
        info->guid = sqlite3_last_insert_rowid(handle->db);
    }

    if (info->type == STORE_DOCTYPE_CONVERSATION) {
        if (SetConversation(handle, newDoc, info) == -1) {
            return -1;
        }
    }

    /* Use the guid as the filename if none has been specified, and update the table */
    if ("" == filename) {
        char buf[32];
        snprintf(buf, sizeof(buf), "bongo-" GUID_FMT, info->guid);
        strncat(info->filename, buf, sizeof(info->filename) - 1);

        stmt = SqlPrepare(handle, 
                          "UPDATE docs SET filename = ?,  info = ? WHERE guid = ?", 
                          &handle->stmts.setFilename);

        if (DStoreBindStr(stmt->stmt, 1, info->filename) ||
            sqlite3_bind_blob(stmt->stmt, 2, 
                              info, DOCINFO_SIZE, SQLITE_STATIC) ||
            sqlite3_bind_int64(stmt->stmt, 3, info->guid) ||
            DStoreStmtExecute(handle, stmt))
        {
            dcode = -1;
        }
            
        DStoreStmtEnd(handle, stmt);
    }
   
    return dcode;
}


int
DStoreDeleteDocInfo(DStoreHandle *handle, uint64_t guid)
{
    DStoreStmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle, "DELETE from docs WHERE guid = ?;", 
                      &handle->stmts.delInfo);
    if (!stmt) {
        return -1;
    }
    if (sqlite3_bind_int64(stmt->stmt, 1, guid) || 
        DStoreStmtExecute(handle, stmt)) 
    {
        dcode = -1;
    }
    DStoreStmtEnd(handle, stmt);
    
    return dcode;
}


int
DStoreGenerateGUID(DStoreHandle *handle, uint64_t *guidOut)
{
    DStoreDocInfo info;

    memset(&info, 0, sizeof(info));
    
    info.filename[0] = 'X';

    if (DStoreSetDocInfo(handle, &info) ||
        DStoreDeleteDocInfo(handle, info.guid)) 
    {
        return -1;
    }
    *guidOut = info.guid;
    return 0;
}


int
DStoreDeleteCollectionContents(DStoreHandle *handle, uint64_t guid)
{
    DStoreStmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle, "DELETE from docs WHERE collection = ?;", 
                      &handle->stmts.delColl);
    if (!stmt) {
        return -1;
    }
    if (sqlite3_bind_int64(stmt->stmt, 1, guid) || 
        DStoreStmtExecute(handle, stmt)) 
    {
        dcode = -1;
    }
    DStoreStmtEnd(handle, stmt);
    
    return dcode;
}

DStoreStmt *
DStoreListColl(DStoreHandle *handle, uint64_t collection, int start, int end)
{
    DStoreStmt *stmt;

    if (start != -1) {
        stmt = SqlPrepare(handle, 
                          "SELECT" DOCINFO_COLS "FROM docs "
                          "WHERE collection = ? LIMIT ?, ?;",
                          &handle->stmts.listCollRange);
    } else {
        stmt = SqlPrepare(handle, 
                          "SELECT" DOCINFO_COLS "FROM docs "
                          "WHERE collection = ?;",
                          &handle->stmts.listColl);
    }
    
    if (!stmt ||
        sqlite3_bind_int64(stmt->stmt, 1, collection) ||
        (start != -1 && (sqlite3_bind_int(stmt->stmt, 2, start) ||
                         sqlite3_bind_int(stmt->stmt, 3, (end - start) + 1)))) {
        DStoreStmtError(handle, stmt);
        return NULL;
    }
    return stmt;
}


DStoreStmt *
DStoreGuidList(DStoreHandle *handle, uint64_t *guids, int numGuids)
{
    char *query;
    char *p;
    int toAlloc;
    int i;
    DStoreStmt *stmt;

    if (handle->stmts.guidList.stmt) {
        sqlite3_finalize(handle->stmts.guidList.stmt);
        handle->stmts.guidList.stmt = NULL;
        
    }    

    assert (numGuids <= STORE_GUID_LIST_MAX);

    toAlloc = strlen ("SELECT guid, info, indexed from docs WHERE guid IN (");
    toAlloc += numGuids * strlen("?, ");
    toAlloc += strlen("); ");
    toAlloc += 1;
    
    query = MemMalloc(toAlloc);
    if (!query) {
        return NULL;
    }
    
    p = query;
    strcpy(p, "SELECT guid, info, indexed from docs WHERE guid IN (");
    p += strlen("SELECT guid, info, indexed from docs WHERE guid IN (");
    
    for(i = 0; i < numGuids - 1; i++) {
        strcpy(p, "?, ");
        p += 3;
    }
    
    if (numGuids > 0) {
        strcpy(p++, "?");
    }
    
    strcpy(p, ");");

    stmt = SqlPrepare(handle, query, &handle->stmts.guidList);

    MemFree(query);

    if (!stmt) {
        return NULL;
    }

    for (i = 0; i < numGuids; i++) {
        if (sqlite3_bind_int64(stmt->stmt, i + 1, guids[i])) {
            DStoreStmtError(handle, stmt);
            return NULL;
        }   
    }
    
    return stmt;
}

int
DStoreSetIndexed(DStoreHandle *handle, int value, uint64_t guid)
{
    DStoreStmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle, "UPDATE docs SET indexed = ? WHERE guid = ?;", 
                      &handle->stmts.setIndexed);
    if (!stmt) {
        return -1;
    }
    if (sqlite3_bind_int(stmt->stmt, 1, value) ||
        sqlite3_bind_int64(stmt->stmt, 2, guid) || 
        DStoreStmtExecute(handle, stmt)) 
    {
        dcode = -1;
    }
    DStoreStmtEnd(handle, stmt);
    
    return dcode;
}


int
DStoreResetTempDocsTable(DStoreHandle *handle)
{
    int dcode = 0;
    DStoreStmt *stmt;

    stmt = SqlPrepare(handle,
                      "DELETE FROM tempdocs;",
                      &handle->stmts.delTempDocsTable);
    if (!stmt) {
        return -1;
    }
    dcode = DStoreStmtExecute(handle, stmt);
    DStoreStmtEnd(handle, stmt);

    return dcode;
}


int
DStoreAddTempDocsInfo(DStoreHandle *handle, DStoreDocInfo *info)
{
    DStoreStmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle,
                      "INSERT INTO tempdocs (guid, info) "
                      "VALUES (?, ?);",
                      &handle->stmts.addTempInfo);
    if (!stmt) {
        return -1;
    }
    if (sqlite3_bind_int64(stmt->stmt, 1, info->guid) ||
        sqlite3_bind_blob(stmt->stmt, 2, info, DOCINFO_SIZE, SQLITE_STATIC) ||
        DStoreStmtExecute(handle, stmt))
    {
        dcode = -1;
    }
    DStoreStmtEnd(handle, stmt);

    return dcode;
}    


int
DStoreMergeTempDocsTable(DStoreHandle *handle)
{
    int dcode = 0;
    DStoreStmt *stmt;

    stmt = SqlPrepare(handle,
                      "UPDATE docs "
                      "SET info = (SELECT info FROM tempdocs "
                      "            WHERE tempdocs.guid=docs.guid) "
                      "WHERE guid IN (SELECT guid FROM tempdocs);",
                      &handle->stmts.mergeTempDocsTable);
    if (!stmt) {
        return -1;
    }
    dcode = DStoreStmtExecute(handle, stmt);
    DStoreStmtEnd(handle, stmt);

    return dcode;
}



DStoreStmt *
DStoreFindProperties(DStoreHandle *handle, uint64_t guid, char *name)
{
    DStoreStmt *stmt;

    if (name) {       
        stmt = SqlPrepare(handle, "SELECT name, value from props "
                                  "WHERE guid = ? AND name = ?;",
                          &handle->stmts.getPropName);
    } else {
        stmt = SqlPrepare(handle, "SELECT name, value from props WHERE guid = ?;",
                          &handle->stmts.getProps);
    }

    if (!stmt) {
        return NULL;
    } 
    if (sqlite3_bind_int64(stmt->stmt, 1, guid) ||
        (name && DStoreBindStr(stmt->stmt, 2, name)))
    {
        DStoreStmtError(handle, stmt);
        return NULL;
    }
    return stmt;
}


/* returns: 1 on success
            0 if no row available,
            -1 on error

   points outVal to the prop value, but this pointer will become invalid upon 
   the next operation on stmt
*/

int
DStorePropStmtStep(DStoreHandle *handle, 
                   DStoreStmt *stmt,
                   StorePropInfo *prop)
{
    int result;

    if (1 != (result = DStoreStmtStep(handle, stmt))) {
        return result;
    }
    prop->name = (char *) sqlite3_column_text(stmt->stmt, 0);
    prop->value = (char *) sqlite3_column_text(stmt->stmt, 1);
    return 1;
}



int 
DStoreGetProperty(DStoreHandle *handle,
                  uint64_t guid, 
                  char *propname, 
                  char *buffer, 
                  size_t size)
{
    DStoreStmt *stmt;
    StorePropInfo prop;
    int dcode;

    stmt = DStoreFindProperties(handle, guid, propname);
    if (!stmt) {
        return -1;
    }
    dcode = DStorePropStmtStep(handle, stmt, &prop);
    if (1 == dcode) {
        strncpy(buffer, prop.value, size);
        buffer[size-1] = 0;
    }
    DStoreStmtEnd(handle, stmt);
    return dcode;
}


int 
DStoreGetPropertySBAppend(DStoreHandle *handle,
                          uint64_t guid, 
                          char *propname, 
                          BongoStringBuilder *sb)
{
    DStoreStmt *stmt;
    StorePropInfo prop;
    int dcode;

    stmt = DStoreFindProperties(handle, guid, propname);
    if (!stmt) {
        return -1;
    }
    dcode = DStorePropStmtStep(handle, stmt, &prop);
    if (1 == dcode) {
        /* FIXME: sbappend should be able to return out-of-memory errors: 
           (bug 174103) */
        BongoStringBuilderAppend(sb, prop.value);
    }
    DStoreStmtEnd(handle, stmt);
    return dcode;
}


int
DStoreSetProperty(DStoreHandle *handle,
                  uint64_t guid,
                  StorePropInfo *prop)
{
    DStoreStmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle, 
                      "INSERT OR REPLACE into props (guid, name, value) "
                      "VALUES (?, ?, ?);", 
                      &handle->stmts.setProp);
    if (!stmt) {
        return -1;
    }

    assert(STORE_IS_EXTERNAL_PROPERTY_TYPE(prop->type));
    if (sqlite3_bind_int64(stmt->stmt, 1, guid) ||
        sqlite3_bind_text(stmt->stmt, 2, 
                          prop->name, strlen(prop->name), SQLITE_TRANSIENT) ||
        sqlite3_bind_text(stmt->stmt, 3, 
                          prop->value, strlen(prop->value), SQLITE_TRANSIENT) ||
        DStoreStmtExecute(handle, stmt))
    {
        dcode = -1;
    }
    DStoreStmtEnd(handle, stmt);
    return dcode;
}

int
DStoreSetPropertySimple(DStoreHandle *handle,
                        uint64_t guid, 
                        const char *name,
                        const char *value)
{
    StorePropInfo prop;

    prop.type = STORE_PROP_EXTERNAL;
    
    prop.name = (char *)name;
    prop.value = (char *)value;
    prop.valueLen = strlen(value);
    
    return DStoreSetProperty(handle, guid, &prop);
}


/** Finds the guid associated with the specified document.
    returns: -1 db error, 0 collection not found, 1 on success */
int
DStoreFindCollectionGuid (DStoreHandle *handle, char *coll, uint64_t *outGuid)
{
    DStoreStmt *stmt;
    int dcode = -1;

    stmt = SqlPrepare(handle, "SELECT guid from docs WHERE filename = ?;",
                      &handle->stmts.getGuidColl);
    if (!stmt || DStoreBindStr(stmt->stmt, 1, coll)) {
        goto done;
    }
    dcode = DStoreStmtStep(handle, stmt);
    if (1 == dcode) {
        *outGuid = sqlite3_column_int64(stmt->stmt, 0);
    }

done:
    DStoreStmtEnd(handle, stmt);
    return dcode;
}


/** Finds the guid associated with the specified file.
    returns: -1 db error, 0 file not found, 1 on success */
int
DStoreFindDocumentGuid (DStoreHandle *handle, char *path, uint64_t *outGuid)
{
    DStoreStmt *stmt;
    int dcode = -1;

    stmt = SqlPrepare(handle, "SELECT guid FROM docs WHERE (filename = ?);",
                      &handle->stmts.getGuidDoc);
    if (!stmt || DStoreBindStr(stmt->stmt, 1, path)) {
        goto done;
    }

    dcode = DStoreStmtStep(handle, stmt);
    if (1 == dcode) {
        *outGuid = sqlite3_column_int64(stmt->stmt, 0);
    }

done:
    if (stmt) {
        DStoreStmtEnd(handle, stmt);
    }
    return dcode;
}


static void
set_docinfo_filename(sqlite3_context *ctx, int argc, sqlite3_value **argv)
{
    DStoreDocInfo info;

    assert(2 == argc);
    assert(sqlite3_value_bytes(argv[0]) == DOCINFO_SIZE);

    memcpy(&info, sqlite3_value_blob(argv[0]), DOCINFO_SIZE);
    strncpy(info.filename, sqlite3_value_text(argv[1]), sizeof(info.filename));
    info.filename[sizeof(info.filename)-1] = 0;
    
    sqlite3_result_blob(ctx, (const void *) &info, DOCINFO_SIZE, SQLITE_TRANSIENT);

}


int
DStoreRenameDocuments(DStoreHandle *handle,
                      const char *oldcoll,
                      const char *newcoll)
{
    DStoreStmt *stmt;
    int dcode = 0;
    char oldprefix[XPL_MAX_PATH+1];
    char newprefix[XPL_MAX_PATH+1];

    snprintf(oldprefix, sizeof(oldprefix), "%s/*", oldcoll);
    snprintf(newprefix, sizeof(newprefix), "%s/", newcoll);

    if (!handle->funcs.setInfoFilename) {
        if (sqlite3_create_function(handle->db, "set_docinfo_filename", 2,
                                    SQLITE_UTF8, NULL, 
                                    set_docinfo_filename, NULL, NULL))
        {
            DStoreStmtError(handle, NULL);
            return -1;
        }
        handle->funcs.setInfoFilename = "set_docinfo_filename";
    }
    
    stmt = SqlPrepare(handle, 
                      "UPDATE docs "
                      "SET filename = ? || substr(filename, ?, 9999), "
                      "    info = set_docinfo_filename(info, "
                                     "? || substr(filename, ?, 9999)) "
                      "WHERE filename glob ?;",
                      &handle->stmts.renameDocs);
    if (!stmt ||
        DStoreBindStr(stmt->stmt, 1, newprefix) ||
        sqlite3_bind_int(stmt->stmt, 2, strlen(oldprefix)) ||
        DStoreBindStr(stmt->stmt, 3, newprefix) ||
        sqlite3_bind_int(stmt->stmt, 4, strlen(oldprefix)) ||
        DStoreBindStr(stmt->stmt, 5, oldprefix) ||

        DStoreStmtExecute(handle, stmt))
    {
        dcode = -1;
    }

    DStoreStmtEnd(handle, stmt);

    return dcode;
}


DStoreStmt *
DStoreFindConversation(DStoreHandle *handle,
                       const char *subject,
                       int32_t modifiedAfter)
{
    DStoreStmt *stmt;

    stmt = SqlPrepare(handle, 
                      "SELECT" CONVINFO_COLS
                      "FROM docs, conversations "
                      "WHERE docs.guid = conversations.guid "
                      "AND conversations.subject = ? "
                      "AND conversations.date >= ?;",
                      &handle->stmts.findConversation);
    if (!stmt ||
        DStoreBindStr(stmt->stmt, 1, subject) ||
        sqlite3_bind_int(stmt->stmt, 2, modifiedAfter))
    {
        DStoreStmtError(handle, stmt);
        return NULL;
    }

    return stmt;
}

int
DStoreGetConversation(DStoreHandle *handle,
                      const char *subject,
                      int32_t modifiedAfter,
                      DStoreDocInfo *result)
{
    DStoreStmt *stmt;
    int ccode = -1;

    stmt = DStoreFindConversation(handle, subject, modifiedAfter);
    if (!stmt) {
        goto done;
    }
    ccode = DStoreInfoStmtStep(handle, stmt, result);
    if (DOCINFO_SIZE == ccode) {
        ccode = 1;
    }

done:
    DStoreStmtEnd(handle, stmt);
    return ccode;
}


static void
test_guid(sqlite3_context *ctx, int argc, sqlite3_value **argv)
{
    uint64_t *guids;
    uint64_t guid;
    DStoreHandle *db;

    /* FIXME: could store length of guids as userdata and do a binary search */

    assert (1 == argc);
    db = (DStoreHandle *) sqlite3_user_data(ctx);
    guids = db->guidsdata.guids;

    if (!guids) {
        sqlite3_result_int(ctx, 1);
        return;
    }

    guid = sqlite3_value_int64(argv[0]);
    
    while (*guids && *guids < guid) {
        ++guids;
    }

    sqlite3_result_int(ctx, *guids == guid);
}


static void
test_info_flags(sqlite3_context *ctx, int argc, sqlite3_value **argv)
{
    const DStoreDocInfo *info;
    uint32_t mask;
    uint32_t flags;

    assert(3 == argc);
    assert(sqlite3_value_bytes(argv[0]) == DOCINFO_SIZE);
    info = (const DStoreDocInfo *) sqlite3_value_blob(argv[0]);
    mask = sqlite3_value_int64(argv[1]);
    flags = sqlite3_value_int64(argv[2]);

    sqlite3_result_int(ctx, (info->flags & mask) == flags);
}


static void
test_headers(sqlite3_context *ctx, int argc, sqlite3_value **argv)
{
    /* test_headers(char *subject, char *senders, char *listid) 
                    
    */
   
    DStoreHandle *db;
    StoreHeaderInfo *headers;
    int count;
    const char *subject;
    const char *senders;
    const char *listid;
    const char *recips;
    int clauseval = 0;

    const char *p;
    char *buf;
    int i;

    assert(4 == argc);

    db = (DStoreHandle *) sqlite3_user_data(ctx);
    headers = db->headersdata.headers;
    count = db->headersdata.count;

    if (0 == count) {
        sqlite3_result_int(ctx, 1);
        return;
    }

    subject = sqlite3_value_text(argv[0]);
    senders = sqlite3_value_text(argv[1]);
    listid = sqlite3_value_text(argv[2]);
    recips = sqlite3_value_text(argv[3]);

    for (i = 0; i < count; i++) {
        if (HEADER_INFO_AND & headers[i].flags) {
            if (!clauseval) {
                sqlite3_result_int(ctx, 0);
                return;
            }
            clauseval = 0;
        } else if (clauseval) {
            continue;
        }
            
        switch (headers[i].type) {
        case HEADER_INFO_FROM:
            p = senders;
            break;
        case HEADER_INFO_RECIP:
            p = recips;
            break;
        case HEADER_INFO_LISTID:
            p = listid;
            break;
        case HEADER_INFO_SUBJECT:
            p = subject;
            break;
        default:
            Log(LOG_ERROR, "test_headers(): Bad header type.");
            continue;
        }

        buf = NULL;
        if (!p) {
            clauseval = 0;
        } else {
            char *q;

            buf = MemStrdup(p);
            if (!buf) {
                return;
            }
            for (q = buf; *q; q++) {
                /* FIXME: not UTF-8 safe */
                *q = tolower(*q);
            }
            clauseval = NULL != strstr(buf, headers[i].value);
        }
        
        if (HEADER_INFO_NOT & headers[i].flags) {
            clauseval = !clauseval;
        }
    }

    sqlite3_result_int(ctx, clauseval);
}


int
DStoreCountConversations(DStoreHandle *handle, 
                         uint32_t requiredSources, uint32_t disallowedSources,
                         StoreHeaderInfo *headers, int headercount,                         
                         uint32_t mask, uint32_t flags)
{
    DStoreStmt *stmt;
    DStoreDocInfo info;
    int dcode = 0;
    int count = 0;

    stmt = SqlPrepare(handle, 
                      "SELECT " CONVINFO_COLS 
                      "FROM docs, conversations "
                      "WHERE docs.guid = conversations.guid "
                      "AND docs.collection = ? "
                      "AND ((? == 0) OR ((conversations.sources & ?2) != 0)) "
                      "AND ((conversations.sources & ?) == 0)" 
                      "AND test_info_flags(docs.info, ?, ?);",
                      &handle->stmts.countConversations);
    
    if (!stmt ||
        sqlite3_bind_int64(stmt->stmt, 1, STORE_CONVERSATIONS_GUID) ||
        sqlite3_bind_int(stmt->stmt, 2, requiredSources) ||
        sqlite3_bind_int(stmt->stmt, 3, disallowedSources) ||
        sqlite3_bind_int64(stmt->stmt, 4, (uint64_t) mask) ||
        sqlite3_bind_int64(stmt->stmt, 5, (uint64_t) flags)) 
    {
        DStoreStmtError(handle, stmt);
        return -1;
    }

    do {
        dcode = DStoreInfoStmtStep(handle, stmt, &info);
        switch (dcode) {
        case 1:
            count++;
            break;
        case 0:
            break;
        case -1:
            count = -1;
            break;
        }
    } while (1 == dcode);

    DStoreStmtEnd(handle, stmt);
    
    return count;
}


/* if <centerGuid> is specified, then the list will return <start> documents
   preceding the guid, and <end> documents following the guid, and also the document
   for the given guid, for a total row count of up to <start> + <end> + 1.
*/

DStoreStmt *
DStoreListConversations(DStoreHandle *handle, 
                        uint32_t requiredSources, uint32_t disallowedSources, 
                        StoreHeaderInfo *headers, int headercount,
                        int start, int end, uint64_t centerGuid, 
                        uint32_t mask, uint32_t flags)
{
    DStoreStmt *stmt;
    
    if (centerGuid) {
        assert (-1 != start && -1 != end);
        
        /* FIXME: This is surely a slow query.  Hopefully it doesn't get 
           used too much... */

        stmt = SqlPrepare(handle,
                          "SELECT" CONVINFO_COLSAS ", conversations.date AS date "
                          "FROM conversations "
                          "JOIN docs " 
                          "        ON docs.guid = conversations.guid "
                          "WHERE conversations.guid IN "
                          "        (SELECT docs.guid "
                          "         FROM conversations "
                          "         JOIN docs ON docs.guid = conversations.guid "
                          "         WHERE conversations.date >= "
                          "                 (SELECT date FROM conversations "
                          "                  WHERE guid = ?6) "
                          "         AND docs.collection = ?1 "
                          "         AND ((?2 == 0) OR "
                          "             (conversations.sources & ?2) != 0) "
                          "         AND ((conversations.sources & ?3) == 0) "
                          "         AND test_info_flags(docs.info, ?7, ?8) "
                          "         ORDER BY conversations.date ASC, docs.guid ASC "
                          "         LIMIT (?5 + 1)) "
                          
                          "UNION "
                          
                          "SELECT" CONVINFO_COLS ", conversations.date " 
                          "FROM conversations "
                          "JOIN docs " 
                          "        ON docs.guid = conversations.guid "
                          "WHERE conversations.guid IN "
                          "        (SELECT docs.guid "
                          "         FROM conversations "
                          "         JOIN docs ON docs.guid = conversations.guid "
                          "         WHERE conversations.date < "
                          "                 (SELECT date FROM conversations "
                          "                  WHERE guid = ?6) "
                          "         AND docs.collection = ?1 "
                          "         AND ((?2 == 0) OR "
                          "             (conversations.sources & ?2) != 0) "
                          "         AND ((conversations.sources & ?3) == 0) "
                          "         AND test_info_flags(docs.info, ?7, ?8) "
                          "         ORDER BY conversations.date DESC, docs.guid DESC "
                          "         LIMIT ?4) "
                          
                          "ORDER BY date DESC, guid DESC;",
                          &handle->stmts.listConversationsCentered);
    } else if (start != -1) {
        stmt = SqlPrepare(handle, 
                          "SELECT" CONVINFO_COLS "from docs, conversations "
                          "WHERE conversations.guid = docs.guid "
                          "AND docs.collection = ?1 "
                          "AND  ((?2 == 0) OR ((conversations.sources & ?2) != 0)) "
                          "AND ((conversations.sources & ?3) == 0) "
                          "AND test_info_flags(docs.info, ?7, ?8) "
                          "ORDER BY conversations.date DESC, docs.guid DESC "
                          "LIMIT ?4, ?5;",
                          &handle->stmts.listConversationsRange);
    } else {
        stmt = SqlPrepare(handle, 
                          "SELECT" CONVINFO_COLS "from docs, conversations "
                          "WHERE conversations.guid = docs.guid "
                          "AND docs.collection = ?1 "
                          "AND ((?2 == 0) OR ((conversations.sources & ?2) != 0)) "
                          "AND ((conversations.sources & ?3) == 0) "
                          "AND test_info_flags(docs.info, ?7, ?8) "
                          "ORDER BY conversations.date DESC, docs.guid DESC;",
                          &handle->stmts.listConversations);
    }
    
    if (!stmt ||
        sqlite3_bind_int64(stmt->stmt, 1, STORE_CONVERSATIONS_GUID) ||
        sqlite3_bind_int(stmt->stmt, 2, requiredSources) ||
        sqlite3_bind_int(stmt->stmt, 3, disallowedSources) ||
        (start != -1 && (sqlite3_bind_int(stmt->stmt, 4, start) ||
                         sqlite3_bind_int(stmt->stmt, 5, 
                                          centerGuid ? end : (end - start + 1)))) ||
        (centerGuid && sqlite3_bind_int(stmt->stmt, 6, centerGuid)) ||
        sqlite3_bind_int64(stmt->stmt, 7, (uint64_t) mask) ||
        sqlite3_bind_int64(stmt->stmt, 8, (uint64_t) flags)) 
    {
        DStoreStmtError(handle, stmt);
        return NULL;
    }
    return stmt;
}


int
DStoreCountMessages(DStoreHandle *handle,
                   uint32_t requiredSources, uint32_t disallowedSources, 
                   StoreHeaderInfo *headers, int headercount,
                   uint32_t mask, uint32_t flags)
{
    DStoreStmt *stmt;
    DStoreDocInfo info;
    int dcode = 0;
    int count = 0;

    stmt = SqlPrepare(handle, 
                      "SELECT " DOCINFO_COLS 
                      "FROM docs "
                      "JOIN conversations "
                      "        ON docs.conversation = conversations.guid "
                      "WHERE ((?1 == 0) OR ((conversations.sources & ?1) != 0)) "
                      "AND ((conversations.sources & ?2) == 0) "
                      "AND test_info_flags(docs.info, ?3, ?4) "
                      "AND test_guid(docs.guid) "
                      "AND test_headers(lower(docs.subject), lower(docs.senders), " 
                      "                 lower(docs.listid)); ",
                      &handle->stmts.countMessages);
    
    if (!stmt ||
        sqlite3_bind_int(stmt->stmt, 1, requiredSources) ||
        sqlite3_bind_int(stmt->stmt, 2, disallowedSources) ||
        sqlite3_bind_int64(stmt->stmt, 3, (uint64_t) mask) ||
        sqlite3_bind_int64(stmt->stmt, 4, (uint64_t) flags)) 
    {
        DStoreStmtError(handle, stmt);
        return -1;
    }

    do {
        dcode = DStoreInfoStmtStep(handle, stmt, &info);
        switch (dcode) {
        case 1:
            count++;
            break;
        case 0:
            break;
        case -1:
            count = -1;
            break;
        }
    } while (1 == dcode);

    DStoreStmtEnd(handle, stmt);
    
    return count;
}


DStoreStmt *
DStoreFindMessages(DStoreHandle *handle,
                   uint32_t requiredSources, uint32_t disallowedSources, 
                   StoreHeaderInfo *headers, int headercount,
                   uint64_t *guidset,
                   int start, int end, uint64_t centerGuid, 
                   uint32_t mask, uint32_t flags)
{
    DStoreStmt *stmt;
    int i;
    int needrecips = 0;
    
    for (i = 0; i < headercount; i++) {
        if (HEADER_INFO_RECIP == headers[i].type) {
            needrecips++;
            break;
        }
    }

    /* userdata for test_header() */
    handle->headersdata.headers = headers;
    handle->headersdata.count = headercount;
    handle->guidsdata.guids = guidset;

    if (centerGuid) {
        /* FIXME - not implemented, returning entire result set for now */
        start = -1;
        end = -1;
    }

    /* FIXME: sqlite lower() function is not UTF-8 safe: */

    if (start != -1) {
        if (needrecips) {
            stmt = 
                SqlPrepare(handle, 
                           "SELECT" DOCINFO_COLS ", conversations.date "
                           "FROM docs "
                           "JOIN conversations "
                           "        ON docs.conversation = conversations.guid "
                           "WHERE ((?1 == 0) OR ((conversations.sources & ?1) != 0)) "
                           "AND ((conversations.sources & ?2) == 0) "
                           "AND test_info_flags(docs.info, ?7, ?8) "
                           "AND test_guid(docs.guid) "
                           "AND test_headers(lower(docs.subject), "
                           "                 lower(docs.senders), "
                           "                 lower(docs.listid), "
                           "                 (SELECT lower(value) "
                           "                  FROM props "
                           "                  WHERE props.guid=docs.guid "
                           "                  AND props.name = 'bongo.to') || "
                           "                 (SELECT lower(value) "
                           "                  FROM props "
                           "                  WHERE props.guid=docs.guid "
                           "                  AND props.name = 'bongo.cc') "
                           ") "
                           "ORDER BY conversations.date DESC, conversations.guid, "
                           "         docs.guid DESC "
                           "LIMIT ?4, ?5;",
                           &handle->stmts.findMessagesRecipRange);
        } else {
            stmt = 
                SqlPrepare(handle, 
                           "SELECT" DOCINFO_COLS ", conversations.date "
                           "FROM docs "
                           "JOIN conversations "
                           "        ON docs.conversation = conversations.guid "
                           "WHERE ((?1 == 0) OR ((conversations.sources & ?1) != 0)) "
                           "AND ((conversations.sources & ?2) == 0) "
                           "AND test_info_flags(docs.info, ?7, ?8) "
                           "AND test_guid(docs.guid) "
                           "AND test_headers(lower(docs.subject), "
                           "                 lower(docs.senders), "
                           "                 lower(docs.listid), "
                           "                 NULL, "
                           ") "
                           "ORDER BY conversations.date DESC, conversations.guid, "
                           "         docs.guid DESC "
                           "LIMIT ?4, ?5;",
                           &handle->stmts.findMessagesRange);
        }
    } else {
        if (needrecips) {
            stmt = 
                SqlPrepare(handle, 
                           "SELECT" DOCINFO_COLS ", conversations.date "
                           "FROM docs "
                           "JOIN conversations "
                           "        ON docs.conversation = conversations.guid "
                           "WHERE ((?1 == 0) OR ((conversations.sources & ?1) != 0)) "
                           "AND ((conversations.sources & ?2) == 0) "
                           "AND test_info_flags(docs.info, ?7, ?8) "
                           "AND test_guid(docs.guid) "
                           "AND test_headers(lower(docs.subject), "
                           "                 lower(docs.senders), "
                           "                 lower(docs.listid), "
                           "                 (SELECT lower(value) "
                           "                  FROM props "
                           "                  WHERE props.guid=docs.guid "
                           "                  AND props.name = 'bongo.to') || "
                           "                 (SELECT lower(value) "
                           "                  FROM props "
                           "                  WHERE props.guid=docs.guid "
                           "                  AND props.name = 'bongo.cc') "
                           ") "
                           "ORDER BY conversations.date DESC, conversations.guid, "
                           "         docs.guid DESC;",
                           &handle->stmts.findMessagesRecipRange);
        } else {
            stmt = 
                SqlPrepare(handle, 
                           "SELECT" DOCINFO_COLS ", conversations.date "
                           "FROM docs "
                           "JOIN conversations "
                           "        ON docs.conversation = conversations.guid "
                           "WHERE ((?1 == 0) OR ((conversations.sources & ?1) != 0)) "
                           "AND ((conversations.sources & ?2) == 0) "
                           "AND test_info_flags(docs.info, ?7, ?8) "
                           "AND test_guid(docs.guid) "
                           "AND test_headers(lower(docs.subject), "
                           "                 lower(docs.senders), "
                           "                 lower(docs.listid), "
                           "                 NULL, "
                           ") "
                          "ORDER BY conversations.date DESC, conversations.guid, " 
                          "         docs.guid DESC;",
                          &handle->stmts.findMessages);
        }
    }
    
    if (!stmt ||
        sqlite3_bind_int(stmt->stmt, 1, requiredSources) ||
        sqlite3_bind_int(stmt->stmt, 2, disallowedSources) ||
        (start != -1 && (sqlite3_bind_int(stmt->stmt, 4, start) ||
                         sqlite3_bind_int(stmt->stmt, 5, 
                                          centerGuid ? end : (end - start + 1)))) ||
        (centerGuid && sqlite3_bind_int(stmt->stmt, 6, centerGuid)) ||
        sqlite3_bind_int64(stmt->stmt, 7, (uint64_t) mask) ||
        sqlite3_bind_int64(stmt->stmt, 8, (uint64_t) flags)) 
    {
        DStoreStmtError(handle, stmt);
        return NULL;
    }
    return stmt;


}


DStoreStmt *
DStoreFindConversationMembers(DStoreHandle *handle, uint64_t conversationId)
{
    DStoreStmt *stmt;
    
    stmt = SqlPrepare(handle, 
                      "SELECT" DOCINFO_COLS "from docs WHERE conversation = ?;",
                      &handle->stmts.findConversationMembers);
    if (!stmt ||
        sqlite3_bind_int64(stmt->stmt, 1, conversationId))
    {
        DStoreStmtError(handle, stmt);
        return NULL;
    }
    
    return stmt;
}


DStoreStmt *
DStoreFindConversationSenders(DStoreHandle *handle, uint64_t conversation)
{
    DStoreStmt *stmt;

    stmt = SqlPrepare(handle, 
                      "SELECT DISTINCT props.value from docs, props "
                      "WHERE docs.conversation=? "
                      "AND props.guid=docs.guid AND props.name='bongo.from';",
                      &handle->stmts.findConversationSenders);
    if (!stmt ||
        sqlite3_bind_int64(stmt->stmt, 1, conversation))
    {
        DStoreStmtError(handle, stmt);
        return NULL;
    }

    return stmt;
}


DStoreStmt *
DStoreFindMailingLists(DStoreHandle *handle, uint32_t requiredSources)
{
    DStoreStmt *stmt;

    stmt = SqlPrepare(handle,
                      "SELECT DISTINCT listid "
                      "FROM docs JOIN conversations "
                      "ON conversations.guid=docs.conversation "
                      "WHERE conversations.sources & ? != 0;",
                      &handle->stmts.findMailingLists);
                      
    if (!stmt ||
        sqlite3_bind_int(stmt->stmt, 1, requiredSources))
    {
        DStoreStmtError(handle, stmt);
        return NULL;
    }

    return stmt;
}


int 
DStoreStrStmtStep(DStoreHandle *handle, DStoreStmt *stmt, const char **strOut)
{
    int result;

    if (1 != (result = DStoreStmtStep(handle, stmt))) {
        return result;
    }

    *strOut = sqlite3_column_text(stmt->stmt, 0);

    return 1;
}



int 
DStoreGuidStmtStep(DStoreHandle *handle, 
                   DStoreStmt *stmt,
                   uint64_t *outGuid)
{
    int result;

    if (1 != (result = DStoreStmtStep(handle, stmt))) {
        return result;
    }

    *outGuid = sqlite3_column_int64(stmt->stmt, 0);

    return 1;
}


/** calendar stuff **/

int
DStoreSetEvent(DStoreHandle *handle, DStoreDocInfo *info)
{
    DStoreStmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle, 
                      "INSERT OR REPLACE into events "
                      "(guid, start, end, flags, uid) "
                      "VALUES (?, ?, ?, ?, ?);",
                      &handle->stmts.setEvent);
    if (!stmt) {
        return -1;
    }

    if (sqlite3_bind_int64(stmt->stmt, 1, info->guid) ||
        DStoreBindStr(stmt->stmt, 2, info->data.event.start) ||
        DStoreBindStr(stmt->stmt, 3, info->data.event.end) ||
        sqlite3_bind_int(stmt->stmt, 4, info->flags) ||
        DStoreBindStr(stmt->stmt, 5, info->data.event.uid) ||

        DStoreStmtExecute(handle, stmt))
    {
        dcode = -1;
    }
    DStoreStmtEnd(handle, stmt);

    return dcode;
}


DStoreStmt *
DStoreFindEvents(DStoreHandle *handle,
                 char *start, char *end,
                 uint64_t calendar, uint32_t mask,
                 char *uid)
{
    DStoreStmt *stmt;

    if (calendar) {
        stmt = SqlPrepare(handle,                    
                          "SELECT " EVENTINFO_COLS
                          "FROM events, links, docs "
                          "WHERE events.guid = links.event "
                          "AND events.start <= ? AND events.end >= ? "
                          "AND links.calendar = ? "
                          "AND events.guid = docs.guid;",
                          &handle->stmts.findEventsCal);
    } else if (uid) {
        stmt = SqlPrepare(handle,                    
                          "SELECT " EVENTINFO_COLS
                          "FROM events, docs "
                          "WHERE events.start <= ? AND events.end >= ? "
                          "AND events.uid = ? "
                          "AND events.guid = docs.guid;",
                          &handle->stmts.findEventsUid);
    } else {
        stmt = SqlPrepare(handle,                    
                          "SELECT " EVENTINFO_COLS
                          "FROM events, docs "
                          "WHERE events.start <= ? AND events.end >= ? "
                          "AND events.guid = docs.guid;",
                          &handle->stmts.findEvents);
    }

    if (!stmt ||
        DStoreBindStr(stmt->stmt, 1, end) || 
        DStoreBindStr(stmt->stmt, 2, start) || 
        (calendar && sqlite3_bind_int64(stmt->stmt, 3, calendar)) ||
        (uid && DStoreBindStr(stmt->stmt, 3, uid)))
    {
        DStoreStmtError(handle, stmt);
        return NULL;
    }
    return stmt;
}


int
DStoreLinkEvent(DStoreHandle *handle, uint64_t calendar, uint64_t event)
{
    DStoreStmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle,
                      "INSERT OR REPLACE INTO links (calendar, event) "
                      "VALUES (?, ?);",
                      &handle->stmts.setLink);

    if (!stmt) {
        return -1;
    }
    if (sqlite3_bind_int64(stmt->stmt, 1, calendar) ||
        sqlite3_bind_int64(stmt->stmt, 2, event) ||
        DStoreStmtExecute(handle, stmt))
    {
        dcode = -1;
    }

    DStoreStmtEnd(handle, stmt);
    return dcode;
}


int 
DStoreUnlinkEvent(DStoreHandle *handle, uint64_t calendar, uint64_t event)
{
    DStoreStmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle,
                      "DELETE FROM links WHERE calendar = ? AND event = ?;",
                      &handle->stmts.delLink);

    if (!stmt) {
        return -1;
    }
    if (sqlite3_bind_int64(stmt->stmt, 1, calendar) ||
        sqlite3_bind_int64(stmt->stmt, 2, event) ||
        DStoreStmtExecute(handle, stmt))
    {
        dcode = -1;
    }

    DStoreStmtEnd(handle, stmt);

    if (dcode) {
        return dcode;
    }

    stmt = SqlPrepare(handle,
                      "DELETE FROM docs "
                      "WHERE guid = ? "
                      "AND 0 == (SELECT count(calendar) FROM links WHERE event = ?);",
                      &handle->stmts.delUnlinkedDoc);
    if (!stmt) {
        return -1;
    }
    if (sqlite3_bind_int64(stmt->stmt, 1, event) ||
        sqlite3_bind_int64(stmt->stmt, 2, event) ||
        DStoreStmtExecute(handle, stmt))
    {
        dcode = -1;
    }

    DStoreStmtEnd(handle, stmt);
    
    if (dcode) {
        return dcode;
    }
    
    
    stmt = SqlPrepare(handle,
                      "DELETE FROM events "
                      "WHERE guid = ? "
                      "AND 0 == (SELECT count(calendar) FROM links WHERE event = ?);",
                      &handle->stmts.delUnlinkedEvent);
    if (!stmt) {
        return -1;
    }
    if (sqlite3_bind_int64(stmt->stmt, 1, event) ||
        sqlite3_bind_int64(stmt->stmt, 2, event) ||
        DStoreStmtExecute(handle, stmt))
    {
        dcode = -1;
    }

    DStoreStmtEnd(handle, stmt);
    
    return dcode;
}


static int UnlinkCalendarHelper(DStoreHandle *handle,
                                const char *sql, 
                                DStoreStmt *stmt, 
                                uint64_t calendar)
{
    int dcode = 0; 

    stmt = SqlPrepare(handle, sql, stmt);
    if (!stmt) {
        return -1;
    }
    if (sqlite3_bind_int64(stmt->stmt, 1, calendar) ||
        DStoreStmtExecute(handle, stmt))
    {
        dcode = -1;
    }

    DStoreStmtEnd(handle, stmt);
    return dcode;
}


int 
DStoreUnlinkCalendar(DStoreHandle *handle, uint64_t calendar)
{
    int dcode;

    /* the first two stmts are for clearing up orphaned events. 
       FIXME: these queries are awkward and must be inefficient. */

    dcode = UnlinkCalendarHelper(handle, 
                                 "DELETE FROM docs "
                                 "WHERE guid IN "
                                 "    (SELECT event FROM links "
                                 "     WHERE calendar = ?1 "
                                 "     EXCEPT "
                                 "     SELECT distinct(event) FROM links "
                                 "     WHERE calendar != ?1) ; ",
                                 &handle->stmts.delOrphanedEventDocs, 
                                 calendar)
        || UnlinkCalendarHelper(handle, 
                                "DELETE FROM events "
                                "WHERE guid IN "
                                "    (SELECT event FROM links "
                                "     WHERE calendar = ?1 "
                                "     EXCEPT "
                                "     SELECT distinct(event) FROM links "
                                "     WHERE calendar != ?1) ; ",
                                &handle->stmts.delOrphanedEvents,
                                calendar)
        || UnlinkCalendarHelper(handle, 
                                "DELETE FROM links WHERE calendar = ?;",
                                &handle->stmts.delCalendarLink, 
                                calendar);
    
    return dcode ? -1 : 0;
}


int
DStoreFindCalendars(DStoreHandle *handle, uint64_t event, BongoArray *calsOut)
{
    DStoreStmt *stmt;
    int dcode = 1;

    stmt = SqlPrepare(handle, "SELECT calendar FROM links WHERE event=?;",
                      &handle->stmts.findCalsEvent);

    if (!stmt) {
        return -1;
    }

    if (sqlite3_bind_int64(stmt->stmt, 1, event)) {
        dcode = -1;
        goto cleanup;
    } 
    
    do {
        dcode = DStoreStmtStep(handle, stmt);        
        if (1 == dcode) {
            uint64_t guid = sqlite3_column_int64(stmt->stmt, 0);
            BongoArrayAppendValue(calsOut, guid);
        }
    } while (1 == dcode);

cleanup:
    DStoreStmtEnd(handle, stmt);

    return dcode;
}


int
DStoreAddJournalEntry(DStoreHandle *handle, uint64_t collection, char *filename)
{
    DStoreStmt *stmt;
    int result;

    stmt = SqlPrepare(handle, 
                      "INSERT into journal (collection, filename) "
                      "VALUES (?, ?);",
                      &handle->stmts.addJournalEntry);
    if (!stmt ||
        sqlite3_bind_int64(stmt->stmt, 1, collection) ||
        DStoreBindStr(stmt->stmt, 2, filename))
    {
        return -1;
    }
    result = DStoreStmtExecute(handle, stmt);
    
    DStoreStmtEnd(handle, stmt);

    return result;
}


int
DStoreRemoveJournalEntry(DStoreHandle *handle, uint64_t collection)
{
    DStoreStmt *stmt;
    int result;

    stmt = SqlPrepare(handle, "DELETE FROM journal WHERE collection=?;",
                      &handle->stmts.delJournalEntry);
    if (!stmt ||
        sqlite3_bind_int64(stmt->stmt, 1, collection))
    {
        return -1;
    }
    result = DStoreStmtExecute(handle, stmt);
    DStoreStmtEnd(handle, stmt);

    return result;
}


DStoreStmt *
DStoreListJournal(DStoreHandle *handle)
{
    DStoreStmt *stmt;

    stmt = SqlPrepare(handle, "SELECT collection, filename FROM journal;", 
                      &handle->stmts.listJournal);

    if (!stmt) {
        DStoreStmtError(handle, stmt);
        return NULL;
    }
    return stmt;
}


int
DStoreJournalStmtStep(DStoreHandle *handle, DStoreStmt *stmt, 
                      uint64_t *outGuid, const char **outFilename)
{
    int result;

    if (1 != (result = DStoreStmtStep(handle, stmt))) {
        return result;
    }

    *outGuid = sqlite3_column_int64(stmt->stmt, 0);
    *outFilename = sqlite3_column_text(stmt->stmt, 1);

    return 1;
}


/** mime cache **/


int
DStoreGetMimeReport(DStoreHandle *handle, uint64_t guid, MimeReport **outReport)
{
    DStoreStmt *stmt;
    int result;
    size_t size;

    stmt = SqlPrepare(handle, "SELECT value FROM mime_cache WHERE guid = ?;",
                      &handle->stmts.getMime);
    if (!stmt ||
        sqlite3_bind_int64(stmt->stmt, 1, guid))
    {
        DStoreStmtError(handle, stmt);
        return -1;
    }

    if (1 != (result = DStoreStmtStep(handle, stmt))) {
        goto finish;
    }

    size = sqlite3_column_bytes(stmt->stmt, 0);
    *outReport = MemMalloc(size);
    if (!*outReport) {
        result = -1;
        goto finish;
    }
    memcpy(*outReport, sqlite3_column_blob(stmt->stmt, 0), size);

finish:
    DStoreStmtEnd(handle, stmt);
    return result;
}


int
DStoreSetMimeReport(DStoreHandle *handle, uint64_t guid, MimeReport *report)
{
    DStoreStmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle, 
                      "INSERT INTO mime_cache (guid, value)"
                      "VALUES (?, ?);",
                      &handle->stmts.setMime);
    if (!stmt ||
        sqlite3_bind_int64(stmt->stmt, 1, guid) ||
        sqlite3_bind_blob(stmt->stmt, 2, report, report->size, SQLITE_STATIC) ||
        DStoreStmtExecute(handle, stmt))
    {
        dcode = -1;
    }

    DStoreStmtEnd(handle, stmt);

    return dcode;
}

int
DStoreClearMimeReport(DStoreHandle *handle, uint64_t guid)
{
    DStoreStmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle, "DELETE from mime_cache WHERE guid = ?;", 
                      &handle->stmts.clearMime);
    if (!stmt) {
        return -1;
    }
    if (sqlite3_bind_int64(stmt->stmt, 1, guid) || 
        DStoreStmtExecute(handle, stmt)) 
    {
        dcode = -1;
    }
    DStoreStmtEnd(handle, stmt);
    
    return dcode;
}
