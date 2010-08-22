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
#include <logger.h>

MsgSQLHandle *
MsgSQLOpen(char *path, BongoMemStack *memstack, int locktimeoutms)
{
	MsgSQLHandle *handle = NULL;
	int create = 0;

#if 0
	if (! sqlite3_threadsafe()) {
		Log(LOG_DEBUG, "msgapi: SQLite needs to be compiled thread-safe. Cannot run.");
		abort();
	}
#endif

	create = access(path, 0);

	if (!(handle = MemMalloc(sizeof(MsgSQLHandle))) ||
	    memset(handle, 0, sizeof(MsgSQLHandle)), 0 ||
	    SQLITE_OK != sqlite3_open(path, &handle->db)) {
		Log(LOG_ERROR, "sql3: Failed to open database \"%s\"", path);
		goto fail;
	}

	Log(LOG_TRACE, "open handle %ld", handle->db);

	handle->memstack = memstack;
	handle->lockTimeoutMs = locktimeoutms;
	handle->transactionDepth = 0;
	XplMutexInit(handle->transactionLock);

	if (create) {
		handle->isNew = TRUE;
	} else {
		handle->isNew = FALSE;
	}

	return handle;

fail:
	if (handle) {
		MsgSQLClose(handle);
	}
	return NULL;
}

void
MsgSQLClose(MsgSQLHandle *handle)
{
	MsgSQLReset(handle);
	XplMutexDestroy(handle->transactionLock);
	if (SQLITE_BUSY == sqlite3_close(handle->db)) {
		Log(LOG_ERROR, "sql3: couldn't close database");
	}
	Log(LOG_TRACE, "sql3: close handle %ld\n\n", handle->db);

	MemFree(handle);
}

void
MsgSQLEndStatement(MsgSQLStatement *_stmt)
{
	int result;
	if (_stmt && _stmt->stmt) {
		result = sqlite3_reset(_stmt->stmt);
		if (result != SQLITE_OK) 
			Log(LOG_ERROR, "sql3: Error ending statement: %d", result);
		Log(LOG_TRACE, "sql3: reset stmt %ld", _stmt->stmt);
	}
}

void
MsgSQLFinalize(MsgSQLStatement *stmt)
{
	int result;
	if (stmt->stmt) {
		result = sqlite3_finalize(stmt->stmt);
		if (result != SQLITE_OK)
			Log(LOG_ERROR, "sql3: Error finalizing statement: %d", result);
		Log(LOG_TRACE, "sql3: final stmt %ld", stmt->stmt);
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
		MsgSQLFinalize(stmt);
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
	int result, reset;
	int count = 1 + handle->lockTimeoutMs / MSGSQL_STMT_SLEEP_MS;
	BOOL locked = FALSE;
	
	// acquire the transaction lock to prevent other people doing stuff 
	XplMutexLock(handle->transactionLock);
	if (locked) {
		Log(LOG_TRACE, "sql3: Acquired lock successfully");
	}

	stmt = MsgSQLPrepare(handle, "BEGIN TRANSACTION;", &handle->stmts.begin);
	if (!stmt) {
		Log(LOG_ERROR, "sql3: Unable to begin transaction");
		return -1;
	}

	do {
		result = sqlite3_step(stmt->stmt);
		Log(LOG_TRACE, "sql3: BEGIN TRAN stmt %ld", stmt->stmt);
		reset = sqlite3_reset(stmt->stmt);
		Log(LOG_TRACE, "sql: - bt reset stmt %ld", stmt->stmt);
	} while (SQLITE_BUSY == result && --count && (XplDelay(MSGSQL_STMT_SLEEP_MS), 1));

	switch (result) {
		case SQLITE_DONE:
			++(handle->transactionDepth);
			return 0;
		case SQLITE_BUSY:
			return -2;
		default:
			Log(LOG_ERROR, "sql3: Database error %d : %s", result, sqlite3_errmsg(handle->db));
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
		Log(LOG_ERROR, "sql3: Unable to prepare end of transaction statement");
		return -1;
	}

	do {
		result = sqlite3_step(stmt->stmt);
		Log(LOG_TRACE, "sql3: COMMIT stmt %ld", stmt->stmt);
		sqlite3_reset(stmt->stmt);
		Log(LOG_TRACE, "sql3:  - ct reset stmt %ld", stmt->stmt);
	} while (SQLITE_BUSY == result && --count && (XplDelay(MSGSQL_STMT_SLEEP_MS), 1));

	if (SQLITE_DONE != result) {
		Log(LOG_ERROR, "sql3: Database commit error %d: %s", result, sqlite3_errmsg(handle->db));
		return -1;
	}

	--handle->transactionDepth;
	XplMutexUnlock(handle->transactionLock);

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
		Log(LOG_ERROR, "sql3: Unable to rollback transaction");
		return -1;
	}

	result = sqlite3_step(stmt->stmt);
	if (SQLITE_DONE != result) {
		Log(LOG_ERROR, "sql3: Transaction rollback failed");
	}
	Log(LOG_TRACE, "sql3: ABORT stmt %ld\n", stmt->stmt);
	sqlite3_reset(stmt->stmt);
	Log(LOG_TRACE, "sql3: - at reset stmt %ld\n", stmt->stmt);
	--handle->transactionDepth;
	XplMutexUnlock(handle->transactionLock);
	
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
	int ret;
	int count = 1 + handle->lockTimeoutMs / MSGSQL_STMT_SLEEP_MS;

	Log(LOG_TRACE, "sql3: PREPARE '%s'", statement);
	// stmt->filter = NULL;
	if (stmt == NULL) return NULL; // we need an output
	
	if (stmt && stmt->stmt) {
		return stmt;
	}
	stmt->query = statement;

	while (count--) {
		ret = sqlite3_prepare(handle->db, statement, -1, &stmt->stmt, NULL);
		Log(LOG_TRACE, "sql3: - new statement %ld", stmt->stmt);
		
		switch (ret) {
			case SQLITE_OK:
				return stmt;
			case SQLITE_BUSY:
				XplDelay(MSGSQL_STMT_SLEEP_MS);
				continue;
			default:
				Log(LOG_ERROR, "sql3: Prepare statement \"%s\" failed; %s", 
					statement, sqlite3_errmsg(handle->db));
				return NULL;
		}
	}
	
	return NULL;
}

int
MsgSQLBindNull(MsgSQLStatement *stmt, int var)
{
	Log(LOG_TRACE, "sql3: - bind null stmt %ld\n", stmt->stmt);
	return sqlite3_bind_null(stmt->stmt, var);
}

int
MsgSQLBindString(MsgSQLStatement *stmt, int var, const char *str, BOOL nullify)
{
	Log(LOG_TRACE, "sql3: - bind str stmt %ld", stmt->stmt);
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
MsgSQLBindInt(MsgSQLStatement *stmt, int var, int value)
{
	Log(LOG_TRACE, "sql3: - bind int stmt %ld", stmt->stmt);
	return sqlite3_bind_int(stmt->stmt, var, value);
}

int
MsgSQLBindInt64(MsgSQLStatement *stmt, int var, uint64_t value)
{
	Log(LOG_TRACE, "sql3: - bind in64 stmt %ld", stmt->stmt);
	return sqlite3_bind_int64(stmt->stmt, var, value);
}

int
MsgSQLExecute(MsgSQLHandle *handle, MsgSQLStatement *_stmt)
{

	sqlite3_stmt *stmt = _stmt->stmt;
	int result;

	result = sqlite3_step(stmt);
	Log(LOG_TRACE, "sql3: EXEC stmt %ld", stmt);
	// FIXME - do I need to reset this??
	if (SQLITE_DONE == result) {
		return 0;
	} else {
		Log(LOG_ERROR, "sql3: Execute failed (%d): %s. Query: %s", result, sqlite3_errmsg(handle->db), _stmt->query);
		return -1;
	}
}

int
MsgSQLQuickExecute(MsgSQLHandle *handle, const char *query)
{
	int result;
	char *error;
	
	result = sqlite3_exec(handle->db, query, NULL, NULL, &error);
	Log(LOG_TRACE, "sql3: QUICK EXEC");
	if (result == SQLITE_OK) return 0;
	
	Log(LOG_ERROR, "sql3: QE failed: %s", error);
	sqlite3_free(error);
	return -1;
}

int
MsgSQLStatementStep(MsgSQLHandle *handle, MsgSQLStatement *_stmt)
{
	sqlite3_stmt *stmt = _stmt->stmt;
	int count = 1 + handle->lockTimeoutMs / MSGSQL_STMT_SLEEP_MS;
	int dbg;
	while (count--) {
		Log(LOG_TRACE, "sql3: - f stmt step %ld", stmt);
		switch ((dbg = sqlite3_step(stmt))) {
			case SQLITE_ROW:
				return 1;
			case SQLITE_DONE:
				return 0;
			case SQLITE_BUSY:
				XplDelay(MSGSQL_STMT_SLEEP_MS);
				continue;
			default:
				Log(LOG_ERROR, "sql3: Step error: %s", sqlite3_errmsg(handle->db));
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
		Log(LOG_TRACE, "sql3: - f stmt results %ld", stmt);
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

uint64_t
MsgSQLLastRowID(MsgSQLHandle *handle)
{
	return sqlite3_last_insert_rowid(handle->db);
}

int
MsgSQLResultInt(MsgSQLStatement *_stmt, int column)
{
	Log(LOG_TRACE, "sql3: - result int %ld", _stmt->stmt);
	return sqlite3_column_int(_stmt->stmt, column);
}

uint64_t
MsgSQLResultInt64(MsgSQLStatement *_stmt, int column)
{
	Log(LOG_TRACE, "sql3: - result int64 %ld", _stmt->stmt);
	return (uint64_t) sqlite3_column_int64(_stmt->stmt, column);
}

// FIXME : need a sensible API to detect errors
int
MsgSQLResultText(MsgSQLStatement *_stmt, int column, char *result, size_t result_size)
{
	const char *out = sqlite3_column_text(_stmt->stmt, column);
	Log(LOG_TRACE, "sql3: - result text %ld", _stmt->stmt);
	if (out != NULL)
		strncpy(result, out, result_size);
	
	return 0;
}

int
MsgSQLResultTextPtr(MsgSQLStatement *_stmt, int column, char **ptr)
{
	*ptr = NULL;
	
	const char *result = sqlite3_column_text(_stmt->stmt, column);
	if (result != NULL) *ptr = g_strdup(result);
	
	return 0;
}
