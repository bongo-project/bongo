#include <config.h>
#include <xpl.h>
#include <nmlib.h>
#include <assert.h>

#include "stored.h"
#include "messages.h"

extern const char *sql_create_cookie_1[];	// defined in sql/create-cookie-1.s.cmake

static MsgSQLHandle *
OpenCookieDB(StoreClient *client)
{
	char path[XPL_MAX_PATH + 1];
	int current_version = 0;
	
	snprintf(path, XPL_MAX_PATH, "%s/cookies.db", MsgGetDir(MSGAPI_DIR_DBF, NULL, 0));
	
	MsgSQLStatement stmt;
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	MsgSQLStatement *schema = NULL;
	
	MsgSQLHandle *cdb = MsgSQLOpen(path, &client->memstack, 3000);
	
	if (MsgSQLBeginTransaction(cdb)) return NULL;
	
	schema = MsgSQLPrepare(cdb, "PRAGMA user_version;", &stmt);
	if (schema == NULL) goto aborttran;
	
	if (! MsgSQLResults(cdb, schema)) goto aborttran;
	
	current_version = MsgSQLResultInt(&stmt, 0);
	MsgSQLEndStatement(&stmt);
	MsgSQLFinalize(&stmt);
	
	if (current_version > 1) {
		Log(LOG_ERROR, "Cookie DB created with newer version of store, cannot open");
	}
	
	if (current_version < 1) {
		if (MsgSQLQuickExecute(cdb, (const char*)sql_create_cookie_1))
			goto aborttran;
		
		MsgSQLReset(cdb);
	}
	
	if (MsgSQLCommitTransaction(cdb)) goto aborttran;
	
	return cdb;
	
aborttran:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(cdb);
	
	return NULL;
}

CCode
StoreCommandCOOKIENEW(StoreClient *client, uint64_t timeout)
{
	assert(STORE_PRINCIPAL_USER == client->principal.type);

	MsgSQLStatement stmt;
	//MsgSQLStatement *ret;
	memset(&stmt, 0, sizeof(MsgSQLStatement));

	char token[50];
	xpl_hash_context context;
	MsgSQLHandle *db = NULL;

	XplHashNew(&context, XPLHASH_MD5);
	snprintf(token, sizeof(token) - 1, "%x%x", 
		(unsigned int) XplGetThreadID(), (unsigned int) time(NULL));
	XplHashWrite(&context, token, strlen(token));
	XplRandomData(token, 8);
	XplHashWrite(&context, token, strlen(token));
	XplHashFinal(&context, XPLHASH_LOWERCASE, token, XPLHASH_MD5_LENGTH);

	uint64_t expiration = time(NULL) + timeout;

	db = OpenCookieDB(client);
	if (db == NULL) goto abort;
	if (MsgSQLBeginTransaction(db)) goto abort;

	MsgSQLPrepare(db, "INSERT INTO cookies (user, cookie, expiration) VALUES (?, ?, ?);", &stmt);
	//ret = MsgSQLPrepare(db, "INSERT INTO cookies (user, cookie, expiration) VALUES (?, ?, ?);", &stmt);
	MsgSQLBindString(&stmt, 1, client->principal.name, FALSE);
	MsgSQLBindString(&stmt, 2, token, FALSE);
	MsgSQLBindInt64(&stmt, 3, expiration);

	if (MsgSQLExecute(db, &stmt)) goto abort;

	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(db))
		goto abort;

	MsgSQLClose(db);

	return ConnWriteF(client->conn, "1000 %.32s\r\n", token);

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(db);
	if (db) MsgSQLClose(db);
	return ConnWriteStr(client->conn, MSG5004INTERNALERR);
}

CCode
StoreCommandCOOKIEDELETE(StoreClient *client, const char *token)
{
	assert(STORE_PRINCIPAL_USER == client->principal.type);

	MsgSQLStatement stmt;
	//MsgSQLStatement *ret;
	memset(&stmt, 0, sizeof(MsgSQLStatement));

	MsgSQLHandle *db = OpenCookieDB(client);
	if (db == NULL) goto abort;
	if (MsgSQLBeginTransaction(db)) goto abort;

	if (token != NULL) {
		MsgSQLPrepare(db, "DELETE FROM cookies WHERE (expiration < ?) OR (user=? AND cookie=?);", &stmt);
		//ret = MsgSQLPrepare(db, "DELETE FROM cookies WHERE (expiration < ?) OR (user=? AND cookie=?);", &stmt);
		MsgSQLBindString(&stmt, 3, token, FALSE);
	} else {
		MsgSQLPrepare(db, "DELETE FROM cookies WHERE expiration < ? OR user=?;", &stmt);
		//ret = MsgSQLPrepare(db, "DELETE FROM cookies WHERE expiration < ? OR user=?;", &stmt);
	}
	MsgSQLBindInt64(&stmt, 1, time(NULL));
	MsgSQLBindString(&stmt, 2, client->principal.name, FALSE);

	if (MsgSQLExecute(db, &stmt)) goto abort;

	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(db))
		goto abort;

	MsgSQLClose(db);

	return ConnWriteStr(client->conn, MSG1000OK);

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(db);
	if (db) MsgSQLClose(db);
	return ConnWriteStr(client->conn, MSG5004INTERNALERR);
}

CCode 
StoreCommandAUTHCOOKIE(StoreClient *client, char *user, char *token, int nouser)
{
	CCode ccode;

	if (StoreAgent.installMode)
		// don't allow cookie logins in installation mode. FIXME: better error message?
		return ConnWriteStr(client->conn, MSG3242BADAUTH);

	if (0 != MsgAuthFindUser(user)) {
		Log(LOG_INFO, "Couldn't find user object for %s\r\n", user);
		
		XplDelay(2000);
		return ConnWriteStr(client->conn, MSG3242BADAUTH);
	}

	MsgSQLStatement stmt;
	MsgSQLStatement *find = NULL;
	int result;
	
	MsgSQLHandle *db = OpenCookieDB(client);
	
	if (MsgSQLBeginTransaction(db)) return -2;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	find = MsgSQLPrepare(db, "SELECT id FROM cookies WHERE user = ? AND cookie = ? AND expiration > ?;", &stmt);
	if (find == NULL) goto abort;
	
	MsgSQLBindString(find, 1, user, FALSE);
	MsgSQLBindString(find, 2, token, FALSE);
	MsgSQLBindInt64(find, 3, time(NULL));
	
	result = MsgSQLResults(db, find);
	if (result < 0) goto abort;
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(db))
		goto abort;

	MsgSQLClose(db);

	if (result == 0) {
		XplDelay(2000);        
		ccode = ConnWriteStr(client->conn, MSG3242BADAUTH);
	} else {
		ccode = SelectUser(client, user, NULL, nouser);
	}

	return ccode;

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(db);
	if (db) MsgSQLClose(db);
	return ConnWriteStr(client->conn, MSG5004INTERNALERR);
}

CCode 
StoreCommandCOOKIELIST(StoreClient *client)
{
	MsgSQLStatement stmt;
	MsgSQLStatement *find = NULL;
	
	MsgSQLHandle *db = OpenCookieDB(client);
	
	if (MsgSQLBeginTransaction(db)) return -2;
	
	memset(&stmt, 0, sizeof(MsgSQLStatement));
	find = MsgSQLPrepare(db, "SELECT cookie, expiration FROM cookies WHERE user = ?", &stmt);
	if (find == NULL) goto abort;
	
	MsgSQLBindString(find, 1, client->principal.name, FALSE);
	
	// loop results
	while (MsgSQLResults(db, &stmt) > 0) {
		char *token;
		
		MsgSQLResultTextPtr(&stmt, 0, &token);
		uint64_t expiration = MsgSQLResultInt64(&stmt, 1);
		
		ConnWriteF(client->conn, "2001 %s " FMT_UINT64_DEC "\r\n", token, expiration);
	}
	
	MsgSQLFinalize(&stmt);
	if (MsgSQLCommitTransaction(db))
		goto abort;

	MsgSQLClose(db);

	return ConnWriteStr(client->conn, MSG1000OK);

abort:
	MsgSQLFinalize(&stmt);
	MsgSQLAbortTransaction(db);
	if (db) MsgSQLClose(db);
	return ConnWriteStr(client->conn, MSG5004INTERNALERR);
}

