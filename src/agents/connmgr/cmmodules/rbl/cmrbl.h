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

#include <ctype.h>
#include <mdb.h>
#include <logger.h>

#include <sqlite3.h>

#include <connmgr.h>

#define PRODUCT_SHORT_NAME  "cmrbl.nlm"
#define PRODUCT_NAME        "Bongo Connection Manager RBL Module"

#define BLOCKED_STRING      "Blocked by RBL %s"

typedef struct _RBLGlobal {
    /* Handles */
    void *logHandle;
    MDBHandle directoryHandle;

    /* Module State */	
    time_t timeStamp;
    int tgid;
    BOOL unloadOK;
    XplSemaphore shutdownSemaphore;
    XplAtomic threadCount;

    struct {
        long last;

        unsigned char datadir[XPL_MAX_PATH + 1];
        unsigned char blockTimeout[256];
        unsigned char allowTimeout[256];
    } config;

    struct {
        unsigned char **hosts;
        unsigned char **comments;
        unsigned long *ids;
        unsigned long count;
        unsigned long allocated;
    } rbl;

    struct {
        sqlite3 *handle;
        XplSemaphore semaphore;

        struct {
            sqlite3_stmt *add;
            sqlite3_stmt *get;
            sqlite3_stmt *clean;
        } stmt;
    } sqlite;
} RBLGlobal;

extern RBLGlobal RBL;

/* cmrbl.c */
void RBLShutdownSigHandler(int Signal);
int _NonAppCheckUnload(void);

EXPORT BOOL CMRBLInit(CMModuleRegistrationStruct *registration, unsigned char *dataDirectory);
EXPORT BOOL RBLShutdown(void);
EXPORT int RBLVerify(ConnMgrCommand *command, ConnMgrResult *result);

/* Config Prefixes */
#define BLOCK_TIMEOUT_PREFIX        "RBLBlockTimeout:"
#define ALLOW_TIMEOUT_PREFIX        "RBLAllowTimeout:"

/* SQL */
#define SQL_CREATE          "BEGIN TRANSACTION;"                            \
                            "CREATE TABLE cache ("                          \
                                "ip INTEGER PRIMARY KEY NOT NULL,"          \
                                "timeout DATETIME,"                         \
                                "block BOOL,"                               \
                                "commentID INTEGER"                         \
                            ");"                                            \
                            "CREATE TABLE comments ("                       \
                                "key INTEGER PRIMARY KEY AUTOINCREMENT,"    \
                                "zone TEXT UNIQUE,"                         \
                                "comment TEXT,"                             \
                                "keep BOOL default 1"                       \
                            ");"                                            \
                            "CREATE INDEX timeout_idx ON cache (timeout);"  \
                            "CREATE INDEX comment_idx ON cache (commentID);"\
                            "END TRANSACTION;"

#define SQL_ROLLBACK        "ROLLBACK;"

#define SQL_ADD             "INSERT OR REPLACE INTO cache (ip, timeout, block, commentID) VALUES (?, DATETIME('now', ?), ?, ?);"
#define SQL_GET             "SELECT cache.block, comments.comment FROM cache INNER JOIN comments ON cache.commentID=comments.key WHERE ip=? AND julianday('now') < julianday(timeout);"
#define SQL_CLEAN           "DELETE FROM cache WHERE julianday('now') >= julianday(timeout);"

#define SQL_BEGIN           "BEGIN TRANSACTION;"
#define SQL_END             "END TRANSACTION;"
#define SQL_SAVE            SQL_CLEAN SQL_END SQL_BEGIN

#define SQL_COMMENTS_CLEAN1 "UPDATE comments SET keep=0;"
#define SQL_COMMENTS_CLEAN2 "UPDATE comments SET keep=1 WHERE zone=?;"
#define SQL_COMMENTS_CLEAN3 "DELETE FROM comments WHERE keep=0"
#define SQL_COMMENTS_ADD    "INSERT INTO comments (zone, comment) VALUES (?, ?);"
#define SQL_COMMENTS_UPDATE "UPDATE comments SET comment=? WHERE zone=?;"
#define SQL_COMMENTS_GET_ID "SELECT key FROM comments WHERE zone=?;"

