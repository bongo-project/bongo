#include <stdio.h>
#include <config.h>
#include <xpl.h>
#include <sqlite3.h>
#include <bongoutil.h>
#include <msgapi.h>
#define LOGGERNAME "msgauth"
#include <logger.h>

#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <time.h>

typedef struct { 
	MsgSQLStatement find_user;
	MsgSQLStatement auth_user;
	MsgSQLStatement add_user;
	MsgSQLStatement set_password;
} MsgAuthStatements;

MsgAuthStatements msgauth_stmts;

int
AuthSqlite_GetDbPath(char *path, size_t size)
{
	return snprintf(path, size, "%s/%s", XPL_DEFAULT_DBF_DIR, "userdb.sqlite");
}

int
AuthSqlite_GenerateHash(const char *username, const char *password, char *result, size_t result_len)
{
	xpl_hash_context ctx;
	
	if (result_len <= XPLHASH_SHA1_LENGTH) return -1;
	
	XplHashNew(&ctx, XPLHASH_SHA1);
	XplHashWrite(&ctx, username, strlen(username));
	XplHashWrite(&ctx, password, strlen(password));
	XplHashFinal(&ctx, XPLHASH_UPPERCASE, result, XPLHASH_SHA1_LENGTH);
	
	result[XPLHASH_SHA1_LENGTH] = '\0';
	return 0;
}

/* returns 0 on success */
int
AuthSqlite_Install(void)
{
	int dcode;
	char path[XPL_MAX_PATH + 1];
	MsgSQLHandle *handle;
	struct stat buf;

	AuthSqlite_GetDbPath(&path, XPL_MAX_PATH);
	if (stat(path, &buf) == 0) {
		// FIXME: db already exists - for now, remove, but could/should be more gentle
		unlink(path);
	}

	handle = MsgSQLOpen(path, NULL, 1000);

	if (NULL == handle) return -2;

	if (MsgSQLBeginTransaction(handle)) goto fail;

	dcode = sqlite3_exec (handle->db, 
			"PRAGMA user_version=0;"
			"CREATE TABLE users (username TEXT DEFAULT NULL,"
			"		password TEXT DEFAULT NULL"
			");"
			"INSERT INTO users (username, password)"
			" VALUES ('admin', 'E03B869D14018868764F6024673D2C8E3DDAFEDA'); ",
			NULL, NULL, NULL);
	if (SQLITE_OK != dcode) goto fail;

	if (MsgSQLCommitTransaction(handle)) goto fail;

	MsgSQLClose(handle);
	return 0;

fail:
	Log(LOG_ERROR, "Error creating database: %s", sqlite3_errmsg(handle->db));

	MsgSQLAbortTransaction(handle);
	MsgSQLClose(handle);
	return -1;
}

int
AuthSqlite_FindUser(const char *user)
{
	MsgSQLStatement *stmt;
	char path[XPL_MAX_PATH + 1];
	MsgSQLHandle *handle;
	int users = -1;

	AuthSqlite_GetDbPath(&path, XPL_MAX_PATH);
	handle = MsgSQLOpen(path, NULL, 1000);

	// Log error?
	if (NULL == handle) {
		Log(LOG_ERROR, "Cannot open user db '%s'", path);
		return 1;
	}

	stmt = MsgSQLPrepare (handle, "SELECT count(username) FROM users WHERE username = ?;",
		&msgauth_stmts.find_user);
	if (NULL == stmt) {
		Log(LOG_ERROR, "Unable to prepare SQL statement in FindUser");
		return 1;
	}
	MsgSQLBindString(stmt, 1, user, TRUE);

	if (MsgSQLResults(handle, stmt) >= 0) {
		// should only have one result column
		users = sqlite3_column_int(stmt->stmt, 0);
	}

	MsgSQLFinalize(stmt);
	MsgSQLClose(handle);

	if (users == 1) {
		// found the user
		return 0;
	}
	
	LogAssertF(users == 0, "User %s defined more than once in the user db", user);
	
	return 1;
}

int
AuthSqlite_VerifyPassword(const char *user, const char *password)
{
	MsgSQLStatement *stmt;
	char path[XPL_MAX_PATH + 1];
	char hash[XPLHASH_SHA1_LENGTH + 1];
	MsgSQLHandle *handle;
	int users = -1;

	if (AuthSqlite_GenerateHash(user, password, hash, XPLHASH_SHA1_LENGTH + 1) != 0) 
		return 1;

	AuthSqlite_GetDbPath(&path, XPL_MAX_PATH);
	handle = MsgSQLOpen(path, NULL, 1000);

	// Log error?
	if (NULL == handle) {
		Log(LOG_ERROR, "Cannot open user db '%s'", path);
		return 2;
	}

	stmt = MsgSQLPrepare (handle, "SELECT count(username) FROM users WHERE username = ? AND password = ?;",
		&msgauth_stmts.auth_user);
	if (NULL == stmt) {
		Log(LOG_ERROR, "Unable to prepare SQL statement in VerifyPassword");
		return 1;
	}
	MsgSQLBindString(stmt, 1, user, TRUE);
	MsgSQLBindString(stmt, 2, hash, TRUE);

	if (MsgSQLResults(handle, stmt) >= 0) {
		// should only have one result column
		users = sqlite3_column_int(stmt->stmt, 0);
	}

	MsgSQLFinalize(stmt);
	MsgSQLClose(handle);

	if (users == 1) {
		return 0; // user exists / password verified
	}
	if (users == 0) {
		return 1; // no such user or password wrong, not an error
	}
	
	Log(LOG_ERROR, "User %s exists multiple times in the user db", user);
	return 3;
}

/* "Write" functions below */

int
AuthSqlite_AddUser(const char *user)
{
	MsgSQLStatement *stmt;
	char path[XPL_MAX_PATH + 1];
	MsgSQLHandle *handle;
	
	AuthSqlite_GetDbPath(&path, XPL_MAX_PATH);
	handle = MsgSQLOpen(path, NULL, 1000);
	if (NULL == handle) {
		Log(LOG_ERROR, "Cannot open user db '%s'", path);
		return -1;
	}
	
	stmt = MsgSQLPrepare (handle,
		"INSERT INTO users (username) VALUES (?);",
		&msgauth_stmts.add_user);
	
	if (NULL == stmt) {
		Log(LOG_ERROR, "Unable to prepare SQL statement in AddUser");
		return 1;
	}
	
	if (MsgSQLBindString(stmt, 1, user, TRUE)) return -2;
	if (MsgSQLExecute(handle, stmt)) return -3;
	
	MsgSQLFinalize(stmt);
	MsgSQLClose(handle);
	
	return 0;
}

int
AuthSqlite_SetPassword(const char *user, const char *password)
{
	MsgSQLStatement *stmt;
	char path[XPL_MAX_PATH + 1];
	char hash[XPLHASH_SHA1_LENGTH + 1];
	MsgSQLHandle *handle;
	
	if (AuthSqlite_GenerateHash(user, password, hash, XPLHASH_SHA1_LENGTH + 1) != 0)
		return -1;
	AuthSqlite_GetDbPath(&path, XPL_MAX_PATH);
	handle = MsgSQLOpen(path, NULL, 1000);
	if (NULL == handle) {
		Log(LOG_ERROR, "Cannot open user db '%s'", path);
		return -2;
	}
	
	stmt = MsgSQLPrepare (handle,
		"UPDATE users SET password = ? WHERE username= ?;",
		&msgauth_stmts.set_password);
	if (NULL == stmt) {
		Log(LOG_ERROR, "Unable to prepare SQL in SetPassword()");
		return 1;
	}
	
	if (MsgSQLBindString(stmt, 1, hash, TRUE)) return -3;
	if (MsgSQLBindString(stmt, 2, user, TRUE)) return -3;
	if (MsgSQLExecute(handle, stmt)) return -3;
	
	MsgSQLFinalize(stmt);
	MsgSQLClose(handle);
	
	return 0;
}

int
AuthSqlite_GetUserStore(const char *user, struct sockaddr_in *store)
{
	memset(store, 0, sizeof(store));
	store->sin_addr.s_addr = inet_addr("127.0.0.1");
	store->sin_family = AF_INET;
	store->sin_port = htons(689);
	return TRUE;
}

int
AuthSqlite_UserList(BongoArray **list)
{
	/* TODO
	BongoArray *userlist;

	userlist = BongoArrayNew(sizeof (char *), 0);

	char *admin = strdup("admin");
	BongoArrayAppendValue(userlist, admin);

	*list = userlist;
	*/
	return TRUE;
}

int
AuthSqlite_InterfaceVersion(void)
{
	return 1;
}

int
main (int argc, char *argv[])
{
	printf("This cannot be run directly");
	return (-1);
}
