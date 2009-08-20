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

// #define DEBUG(x,y)	printf("sql3: " x, y);
#define DEBUG(x,y)		;

MsgSQLHandle *
MsgSQLOpen(char *path, BongoMemStack *memstack, int locktimeoutms)
{
	MsgSQLHandle *handle = NULL;
	int create = 0;

#if 0
	if (! sqlite3_threadsafe()) {
		XplConsolePrintf("msgapi: SQLite needs to be compiled thread-safe. Cannot run.\n");
		abort();
	}
#endif

	create = access(path, 0);

	if (!(handle = MemMalloc(sizeof(MsgSQLHandle))) ||
	    memset(handle, 0, sizeof(MsgSQLHandle)), 0 ||
	    SQLITE_OK != sqlite3_open(path, &handle->db)) {
		// FIXME Logging
		XplConsolePrintf("msgapi: Failed to open database \"%s\".\r\n", path);
		goto fail;
	}

	DEBUG("open handle %ld\n", handle->db)

	handle->memstack = memstack;
	handle->lockTimeoutMs = locktimeoutms;
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
		// FIXME logging
		XplConsolePrintf ("msgapi: couldn't close database\r\n");
	}
	DEBUG("close handle %ld\n\n", handle->db)

	MemFree(handle);
}

void
MsgSQLEndStatement(MsgSQLStatement *_stmt)
{
	int result;
	if (_stmt && _stmt->stmt) {
		result = sqlite3_reset(_stmt->stmt);
		if (result != SQLITE_OK) 
			printf("Error ending statement: %d\n", result);
		DEBUG("reset stmt %ld\n", _stmt->stmt)
	}
}

void
MsgSQLFinalize(MsgSQLStatement *stmt)
{
	int result;
	if (stmt->stmt) {
		result = sqlite3_finalize(stmt->stmt);
		if (result != SQLITE_OK)
			printf("Error finalizing statement: %d\n", result);
		DEBUG("final stmt %ld\n", stmt->stmt)
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
	if (handle->transactionDepth) {
		DEBUG("* Need to acquire lock already taken\n", NULL)
		locked = TRUE;
	}
	XplMutexLock(handle->transactionLock);
	if (locked) {
		DEBUG("* Acquired lock successfully\n", NULL)
	}
/*
	if (handle->transactionDepth) {
		// FIXME: ensure it's an exclusive transaction
		XplConsolePrintf("IMMEDIATE ERROR: transaction started on handler in use\n");
		abort ();
		return 0;
	}
*/
	
	stmt = MsgSQLPrepare(handle, "BEGIN TRANSACTION;", &handle->stmts.begin);
	if (!stmt) {
		//DStoreStmtError(handle, stmt);
		return -1;
	}

	do {
		result = sqlite3_step(stmt->stmt);
		DEBUG("BEGIN TRAN stmt %ld\n", stmt->stmt)
		reset = sqlite3_reset(stmt->stmt);
		DEBUG(" - bt reset stmt %ld\n", stmt->stmt)
	} while (SQLITE_BUSY == result && --count && (XplDelay(MSGSQL_STMT_SLEEP_MS), 1));

	switch (result) {
		case SQLITE_DONE:
			++(handle->transactionDepth);
			return 0;
		case SQLITE_BUSY:
			return -2;
		default:
			printf("Database error %d : %s\n", result, sqlite3_errmsg(handle->db));
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
		DEBUG("COMMIT stmt %ld\n", stmt->stmt)
		sqlite3_reset(stmt->stmt);
		DEBUG(" - ct reset stmt %ld\n", stmt->stmt)
	} while (SQLITE_BUSY == result && --count && (XplDelay(MSGSQL_STMT_SLEEP_MS), 1));

	--handle->transactionDepth;
	XplMutexUnlock(handle->transactionLock);

	if (SQLITE_DONE != result) {
		// DStoreStmtError(handle, stmt);
		printf("Database commit error %d: %s\n", result, sqlite3_errmsg(handle->db));
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
	DEBUG("ABORT stmt %ld\n", stmt->stmt)
	sqlite3_reset(stmt->stmt);
	DEBUG(" - at reset stmt %ld\n", stmt->stmt)
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

	DEBUG("PREPARE '%s'\n", statement)
	// stmt->filter = NULL;
	if (stmt == NULL) return NULL; // we need an output
	
	if (stmt && stmt->stmt) {
		return stmt;
	}
	stmt->query = statement;

	while (count--) {
		ret = sqlite3_prepare(handle->db, statement, -1, &stmt->stmt, NULL);
		DEBUG(" - new statement %ld\n", stmt->stmt)
		
		switch (ret) {
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
MsgSQLBindNull(MsgSQLStatement *stmt, int var)
{
	DEBUG(" - bind null stmt %ld\n", stmt->stmt)
	return sqlite3_bind_null(stmt->stmt, var);
}

int
MsgSQLBindString(MsgSQLStatement *stmt, int var, const char *str, BOOL nullify)
{
	DEBUG(" - bind str stmt %ld\n", stmt->stmt)
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
	DEBUG(" - bind int stmt %ld\n", stmt->stmt)
	return sqlite3_bind_int(stmt->stmt, var, value);
}

int
MsgSQLBindInt64(MsgSQLStatement *stmt, int var, uint64_t value)
{
	DEBUG(" - bind in64 stmt %ld\n", stmt->stmt)
	return sqlite3_bind_int64(stmt->stmt, var, value);
}

int
MsgSQLExecute(MsgSQLHandle *handle, MsgSQLStatement *_stmt)
{

	sqlite3_stmt *stmt = _stmt->stmt;
	int result;

	result = sqlite3_step(stmt);
	DEBUG("EXEC stmt %ld\n", stmt)
	// FIXME - do I need to reset this??
	if (SQLITE_DONE == result) {
		return 0;
	} else {
		XplConsolePrintf("Sql (%d): %s. Query: %s\r\n", result, sqlite3_errmsg(handle->db), _stmt->query);
		return -1;
	}
}

int
MsgSQLQuickExecute(MsgSQLHandle *handle, const char *query)
{
	int result;
	char *error;
	
	result = sqlite3_exec(handle->db, query, NULL, NULL, &error);
	DEBUG("QUICK EXEC\n", NULL)
	if (result == SQLITE_OK) return 0;
	
	XplConsolePrintf("Sql QE: %s\r\n", error);
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
		DEBUG(" - f stmt step %ld\n", stmt)
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
		DEBUG(" - f stmt results %ld\n", stmt)
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
	DEBUG(" - result int %ld\n", _stmt->stmt)
	return sqlite3_column_int(_stmt->stmt, column);
}

uint64_t
MsgSQLResultInt64(MsgSQLStatement *_stmt, int column)
{
	DEBUG(" - result int64 %ld\n", _stmt->stmt)
	return (uint64_t) sqlite3_column_int64(_stmt->stmt, column);
}

// FIXME : need a sensible API to detect errors
int
MsgSQLResultText(MsgSQLStatement *_stmt, int column, char *result, size_t result_size)
{
	const char *out = sqlite3_column_text(_stmt->stmt, column);
	DEBUG(" - result text %ld\n", _stmt->stmt)
	if (out != NULL)
		strncpy(result, out, result_size);
	
	return 0;
}

int
MsgSQLResultTextPtr(MsgSQLStatement *_stmt, int column, char **ptr)
{
	*ptr = strdup(sqlite3_column_text(_stmt->stmt, column));
	return 0;
}
