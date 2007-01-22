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
#include <sqlite3.h>
#include <assert.h>

#include "stored.h"

/****************************************************

    
   
*****************************************************/


struct _AlarmDBHandle {
    sqlite3 *db;

    struct {
        sqlite3_stmt *begin;        /* must be first in struct (see Reset() below) */

        sqlite3_stmt *setAlarm;
        sqlite3_stmt *delAlarm;
        sqlite3_stmt *listAlarms;

        sqlite3_stmt *end;          /* must be last in struct */
    } stmts;

    BongoMemStack *memstack;
};


static void
AlarmDBReset(AlarmDBHandle *handle)
{
    sqlite3_stmt **stmt;

    for (stmt = &handle->stmts.begin;
         stmt <= &handle->stmts.end;
         stmt++)
    {
        if (*stmt) {
            sqlite3_finalize(*stmt);
        }
    }
}


static int
CreateAlarmDB(AlarmDBHandle *handle)
{
    int dcode;

    /* Note that alarms_idx is not unique.   */

    dcode = 
        sqlite3_exec (handle->db,
                      "CREATE TABLE alarms (store TEXT NOT NULL, "
                                           "guid INTEGER NOT NULL, "
                                           "time INTEGER, "
                                           "summary TEXT NOT NULL, "
                                           "email TEXT, "
                                           "sms TEXT); "
                      "CREATE INDEX alarms_idx ON alarms (store, guid); "
                      "CREATE INDEX time_idx ON alarms (time); "
                      ,
                      NULL, NULL, NULL);
    
    if (SQLITE_OK != dcode) {
        printf("Couldn't create alarm db: %s\n", sqlite3_errmsg(handle->db));
        return -1;
    }

    return 0;
}


static sqlite3_stmt *
SqlPrepare(AlarmDBHandle *handle, char *statement, sqlite3_stmt **stmt)
{
    int count = 30;

    if (*stmt) {
        return *stmt;
    }
    
    while (count--) {
        switch (sqlite3_prepare(handle->db, statement, -1, stmt, NULL)) {
        case SQLITE_OK:
            return *stmt;
        case SQLITE_BUSY:
            XplDelay(250);
            continue;
        default:
            XplConsolePrintf("SQL Prepare statement \"%s\" failed; %s\r\n", 
                             statement, sqlite3_errmsg(handle->db));
            return NULL;
        }
    }
    XplConsolePrintf("Sql Prepare failed because db is busy.\r\n");
    return NULL;
}


static int
SqlBindStr(sqlite3_stmt *stmt, int var, const char *str)
{
    if (str) {
        return sqlite3_bind_text(stmt, var, str, strlen(str), SQLITE_STATIC);
    } else {
        return sqlite3_bind_text(stmt, var, "", 0, SQLITE_STATIC);
    }
}


static int
SqlBindStrOrNull(sqlite3_stmt *stmt, int var, const char *str)
{
    if (str) {
        return sqlite3_bind_text(stmt, var, str, strlen(str), SQLITE_STATIC);
    } else {
        return sqlite3_bind_null(stmt, var);
    }
}


AlarmDBHandle * 
AlarmDBOpen(BongoMemStack *memstack)
{
    char path[XPL_MAX_PATH + 1];
    AlarmDBHandle *handle = NULL;
    int create = 0;
    struct stat sb;

    if (stat(StoreAgent.store.systemDir, &sb)) {
        if (XplMakeDir(StoreAgent.store.systemDir)) {
            printf("Error creating system store: %s\n", strerror(errno));
            return NULL;
        }
    }

    snprintf(path, sizeof(path), "%s/alarms.db", StoreAgent.store.systemDir);
    path[sizeof(path)-1] = 0;

    create = access(path, 0);

    if (!(handle = MemMalloc(sizeof(struct _AlarmDBHandle))) ||
        memset(handle, 0, sizeof(struct _AlarmDBHandle)), 0 ||
        SQLITE_OK != sqlite3_open(path, &handle->db))
    {
        printf("bongostore: Failed to open alarm store \"%s\".\r\n", path);
        goto fail;
    }

    handle->memstack = memstack;

    if (create && CreateAlarmDB(handle)) {
        printf("Couldn't open db");
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
AlarmDBClose(AlarmDBHandle *handle)
{
    AlarmDBReset(handle);
    if (SQLITE_BUSY == sqlite3_close(handle->db)) {
        XplConsolePrintf ("bongostore: couldn't close alarm database!\r\n");
    }

    MemFree(handle);
}


int
AlarmStmtStep(AlarmDBHandle *handle, AlarmStmt *_stmt)
{
    sqlite3_stmt *stmt = (sqlite3_stmt *) _stmt;
    int count = 30;
    
    while (count--) {
        switch (sqlite3_step(stmt)) {
        case SQLITE_ROW:
            return 1;
        case SQLITE_DONE:
            return 0;
        case SQLITE_BUSY:
            XplDelay(333);
            continue;
        default:
            XplConsolePrintf("Sql: %s\r\n", sqlite3_errmsg(handle->db));
            return -1;
        }
    }
    XplConsolePrintf("Sql Busy.\r\n");
    return -1;   
}
              

void
AlarmStmtEnd(AlarmDBHandle *handle, AlarmStmt *_stmt)
{
    sqlite3_stmt *stmt = (sqlite3_stmt *) _stmt;

    sqlite3_reset(stmt);
}


AlarmInfoStmt *
AlarmDBListAlarms(AlarmDBHandle *handle, 
                  uint64_t start, uint64_t end)
{
    sqlite3_stmt *stmt;

    stmt = SqlPrepare(handle, 
                      "SELECT store, guid, time, summary, email, sms "
                      "FROM alarms "
                      "WHERE time >= ?1 AND time < ?2;",
                      &handle->stmts.listAlarms);
    if (!stmt || 
        sqlite3_bind_int64(stmt, 1, start) ||
        sqlite3_bind_int64(stmt, 2, end))
    {
        return NULL;
    }

    return (AlarmInfoStmt *) stmt;
}


#define GET_COL(stmt, colno, var)                                           \
{                                                                           \
    int len = sqlite3_column_bytes(stmt, colno);                            \
    if (!len) {                                                             \
        var = NULL;                                                         \
    } else {                                                                \
        var = BongoMemStackPushStr(handle->memstack,                         \
                                  sqlite3_column_text(stmt, colno));        \
        if (!var) {                                                         \
            return -1;                                                      \
        }                                                                   \
    }                                                                       \
}


int 
AlarmInfoStmtStep(AlarmDBHandle *handle, AlarmInfoStmt *_stmt, AlarmInfo *info)
{
    int dcode;
    sqlite3_stmt *stmt = (sqlite3_stmt *) _stmt;

    dcode = AlarmStmtStep(handle, _stmt);
    if (1 != dcode) {
        return dcode;
    }

    GET_COL(stmt, 0, info->store);
    info->guid = sqlite3_column_int64(stmt, 1);
    info->time = BongoCalTimeNewFromUint64(sqlite3_column_int64(stmt, 2), FALSE, NULL);
    GET_COL(stmt, 3, info->summary);
    GET_COL(stmt, 4, info->email);
    GET_COL(stmt, 5, info->sms);

    return 1;
}


int
AlarmDBSetAlarm(AlarmDBHandle *handle, AlarmInfo *info)
{
    sqlite3_stmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle, 
                      "INSERT INTO alarms (store, guid, time, summary, email, sms) "
                      "VALUES (?, ?, ?, ?, ?, ?);",
                      &handle->stmts.setAlarm);
    if (!stmt) {
        return -1;
    }
    if (SqlBindStr(stmt, 1, info->store) ||
        sqlite3_bind_int64(stmt, 2, info->guid) ||
        sqlite3_bind_int64(stmt, 3, BongoCalTimeUtcAsUint64(info->time)) ||
        SqlBindStr(stmt, 4, info->summary) ||
        SqlBindStrOrNull(stmt, 5, info->email) ||
        SqlBindStrOrNull(stmt, 6, info->sms) ||
        SQLITE_DONE != sqlite3_step(stmt))
    {
        dcode = -1;
    }
    
    AlarmStmtEnd(handle, (AlarmStmt *) stmt);
    
    return dcode;
}


int
AlarmDBDeleteAlarms(AlarmDBHandle *handle, const char *store, uint64_t guid)
{
    sqlite3_stmt *stmt;
    int dcode = 0;

    stmt = SqlPrepare(handle, 
                      "DELETE FROM alarms WHERE store = ? AND guid = ?;",
                      &handle->stmts.delAlarm);
    if (!stmt) {
        return -1;
    }
    if (SqlBindStr(stmt, 1, store) || 
        sqlite3_bind_int64(stmt, 2, guid) ||
        SQLITE_DONE != sqlite3_step(stmt))
    {
        dcode = -1;
    }
    
    sqlite3_reset(stmt);
    
    return dcode;
}


