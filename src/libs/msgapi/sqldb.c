/* Bongo Project licensing applies to this file, see COPYING
 * (C) 2006 Novell
 * (C) 2007 Alex Hudson
 */

/** \file
 *  SQLite Helper API
 */

/* This code all adapted from the Store, but made generic.
 * Eventually, the store should probably use this
 */

#include <xpl.h>
#include <msgapi.h>
#include <sqlite3.h>

MsgSQLHandle *
MsgSQLOpen(char *path, BongoMemStack *memstack, int locktimeoutms)
{
	MsgSQLHandle *handle = NULL;
	int create = 0;

	create = access(path, 0);

	if (!(handle = MemMalloc(sizeof(MsgSQLHandle))) ||
	    memset(handle, 0, sizeof(MsgSQLHandle)), 0 ||
	    SQLITE_OK != sqlite3_open(path, &handle->db)) {
		// FIXME Logging
		XplConsolePrintf("msgapi: Failed to open database \"%s\".\r\n", path);
		goto fail;
	}

	handle->memstack = memstack;
	handle->lockTimeoutMs = locktimeoutms;

// FIXME
/*	if (create && DStoreCreateDB(handle)) {
		printf("Couldn't open db");
		goto fail;
	}

	if (UpgradeDB(handle)) {
		goto fail;
	}
*/
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
MsgSQLClose(MsgSQLHandle *handle)
{
	MsgSQLReset(handle);
	if (SQLITE_BUSY == sqlite3_close(handle->db)) {
		// FIXME logging
		XplConsolePrintf ("msgapi: couldn't close database\r\n");
	}

	MemFree(handle);
}

void
MsgSQLFinalize(MsgSQLStatement *stmt)
{
	if (stmt->stmt) {
		sqlite3_finalize(stmt->stmt);
		stmt->stmt = NULL;
	}
}

void
MsgSQLReset(MsgSQLHandle *handle)
{
	MsgSQLStatement *stmt;

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

BongoMemStack *
MsgSQLGetMemStack(MsgSQLHandle *handle)
{
	return handle->memstack;
}

void 
MsgSQLSetMemStack(MsgSQLHandle *handle, BongoMemStack *memstack)
{
	handle->memstack = memstack;
}


void
MsgSQLSetLockTimeout(MsgSQLHandle *handle, int timeoutms)
{
	handle->lockTimeoutMs = timeoutms;
}

// returns 0 on success, -2 db busy, -1 on error
int
MsgSQLBeginTransaction(MsgSQLHandle *handle)
{
	MsgSQLStatement *stmt;
	int result;
	int count = 1 + handle->lockTimeoutMs / MSGSQL_STMT_SLEEP_MS;

	if (handle->transactionDepth) {
		/* FIXME: ensure it's an exclusive transaction */
		abort ();
		return 0;
	}

	stmt = MsgSQLPrepare(handle, "BEGIN EXCLUSIVE TRANSACTION;", &handle->stmts.begin);
	if (!stmt) {
		//DStoreStmtError(handle, stmt);
		return -1;
	}

	do {
		result = sqlite3_step(stmt->stmt);
		sqlite3_reset(stmt->stmt);
	} while (SQLITE_BUSY == result && --count && (XplDelay(MSGSQL_STMT_SLEEP_MS), 1));

	switch (result) {
		case SQLITE_DONE:
			++handle->transactionDepth;
			return 0;
		case SQLITE_BUSY:
			return -2;
		default:
			//DStoreStmtError(handle, stmt);
			return -1;
	}
}

// returns 0 on success, -1 on error
int
MsgSQLCommitTransaction(MsgSQLHandle *handle)
{
	MsgSQLStatement *stmt;
	int result;
	int count = 1 + handle->lockTimeoutMs / MSGSQL_STMT_SLEEP_MS;

	stmt = MsgSQLPrepare(handle, "END TRANSACTION;", &handle->stmts.end);
	if (!stmt) {
		// DStoreStmtError(handle, stmt);
		return -1;
	}

	do {
		result = sqlite3_step(stmt->stmt);
		sqlite3_reset(stmt->stmt);
	} while (SQLITE_BUSY == result && --count && (XplDelay(MSGSQL_STMT_SLEEP_MS), 1));

	--handle->transactionDepth;

	if (SQLITE_DONE != result) {
		// DStoreStmtError(handle, stmt);
		return -1;
	}
	return 0;
}

// returns 0 on success, -1 on error
int
MsgSQLAbortTransaction(MsgSQLHandle *handle)
{
	MsgSQLStatement *stmt;
	int result;

	stmt = MsgSQLPrepare(handle, "ROLLBACK TRANSACTION;", &handle->stmts.abort);
	if (!stmt) {
		// DStoreStmtError(handle, stmt);
		return -1;
	}

	result = sqlite3_step(stmt->stmt);
	if (SQLITE_DONE != result) {
		// DStoreStmtError(handle, stmt);
	}
	sqlite3_reset(stmt->stmt);
	--handle->transactionDepth;
	return SQLITE_DONE == result ? 0 : -1;
}

int
MsgSQLCancelTransactions(MsgSQLHandle *handle)
{
	while (handle->transactionDepth) {
		if (MsgSQLAbortTransaction(handle)) {
			return -1;
		}
	}
	return 0;
}

MsgSQLStatement *
MsgSQLPrepare(MsgSQLHandle *handle, const char *statement, MsgSQLStatement *stmt)
{
	int count = 1 + handle->lockTimeoutMs / MSGSQL_STMT_SLEEP_MS;

	// stmt->filter = NULL;
	if (stmt->stmt) {
		return stmt;
	}

	while (count--) {
		switch (sqlite3_prepare(handle->db, statement, -1, &stmt->stmt, NULL)) {
			case SQLITE_OK:
				return stmt;
			case SQLITE_BUSY:
				XplDelay(MSGSQL_STMT_SLEEP_MS);
				continue;
			default:
				// FIXME: should be logging here.
				XplConsolePrintf("SQL Prepare statement \"%s\" failed; %s\r\n", 
					statement, sqlite3_errmsg(handle->db));
				return NULL;
		}
	}
	
	return NULL;
}

int
MsgSQLBindString(MsgSQLStatement *stmt, int var, const char *str, BOOL nullify)
{
	if (str) {
		return sqlite3_bind_text(stmt->stmt, var, str, strlen(str), SQLITE_STATIC);
	} else {
		if (nullify) {
			return sqlite3_bind_null(stmt->stmt, var);
		} else {
			return sqlite3_bind_text(stmt->stmt, var, "", 0, SQLITE_STATIC);
		}
	}
}

int
MsgSQLExecute(MsgSQLHandle *handle, MsgSQLStatement *_stmt)
{

	sqlite3_stmt *stmt = _stmt->stmt;
	int result;

	result = sqlite3_step(stmt);
	if (SQLITE_DONE == result) {
		return 0;
	} else {
		XplConsolePrintf("Sql (%d): %s\r\n", result, sqlite3_errmsg(handle->db));
		return -1;
	}
}

int
MsgSQLStatementStep(MsgSQLHandle *handle, MsgSQLStatement *_stmt)
{
	sqlite3_stmt *stmt = _stmt->stmt;
	int count = 1 + handle->lockTimeoutMs / MSGSQL_STMT_SLEEP_MS;
	int dbg;
	while (count--) {
		switch ((dbg = sqlite3_step(stmt))) {
			case SQLITE_ROW:
				return 1;
			case SQLITE_DONE:
				return 0;
			case SQLITE_BUSY:
				XplDelay(MSGSQL_STMT_SLEEP_MS);
				continue;
			default:
				XplConsolePrintf("Sql: %s", sqlite3_errmsg(handle->db));
				return -1;
		}
	}

	return -1;
}

int
MsgSQLResults(MsgSQLHandle *handle, MsgSQLStatement *_stmt)
{
	sqlite3_stmt *stmt = _stmt->stmt;
	int result;
	int count = 1 + handle->lockTimeoutMs / MSGSQL_STMT_SLEEP_MS;

	while (count--) {
		result = sqlite3_step(stmt);
		switch (result) {
			case SQLITE_DONE:
				return 0; // no more results
				break;
			case SQLITE_ROW:
				return 1;
				break;
			case SQLITE_BUSY:
				XplDelay(MSGSQL_STMT_SLEEP_MS);
				continue;
			default:
				// FIXME
				//XplConsolePrintf("SQL Prepare statement \"%s\" failed; %s\r\n", 
				// statement, sqlite3_errmsg(handle->db));
				return -1;
		}
	}

	return -1;
}
