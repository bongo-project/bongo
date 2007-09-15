#include <stdio.h>
#include <config.h>
#include <xpl.h>
#include <sqlite3.h>
#include <bongoutil.h>
#include <msgapi.h>

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

	// FIXME: need to store passwords as salted hashes, really
	dcode = sqlite3_exec (handle->db, 
			"PRAGMA user_version=0;"
			"CREATE TABLE users (username TEXT DEFAULT NULL,"
			"		password TEXT DEFAULT NULL,"
			"		email TEXT DEFAULT NULL"
			");"
			"INSERT INTO users (username, password, email)"
			" VALUES ('admin', 'bongo', 'admin'); ",
			NULL, NULL, NULL);
	if (SQLITE_OK != dcode) goto fail;

	if (MsgSQLCommitTransaction(handle)) goto fail;

	MsgSQLClose(handle);
	return 0;

fail:
	// FIXME:  logging
	printf("MsgAuthInitDB error: %s\n", sqlite3_errmsg(handle->db));

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
	if (NULL == handle) return FALSE;

	stmt = MsgSQLPrepare (handle, "SELECT count(username) FROM users WHERE username = ?;",
		&msgauth_stmts.find_user);
	MsgSQLBindString(stmt, 1, user, TRUE);

	if (MsgSQLResults(handle, stmt) >= 0) {
		// should only have one result column
		users = sqlite3_column_int(stmt->stmt, 0);
	}	

	MsgSQLFinalize(stmt);
	MsgSQLClose(handle);

	// one and only one user - any other condition is 'bad'...
	if (users == 1) return TRUE;
	
	return FALSE;
}

int
AuthSqlite_VerifyPassword(const char *user, const char *password)
{
	MsgSQLStatement *stmt;
	char path[XPL_MAX_PATH + 1];
	MsgSQLHandle *handle;
	int users = -1;

	AuthSqlite_GetDbPath(&path, XPL_MAX_PATH);
	handle = MsgSQLOpen(path, NULL, 1000);

	// Log error?
	if (NULL == handle) return 2;

	stmt = MsgSQLPrepare (handle, "SELECT count(username) FROM users WHERE username = ? AND password = ?;",
		&msgauth_stmts.auth_user);
	MsgSQLBindString(stmt, 1, user, TRUE);
	MsgSQLBindString(stmt, 2, password, TRUE);

	if (MsgSQLResults(handle, stmt) >= 0) {
		// should only have one result column
		users = sqlite3_column_int(stmt->stmt, 0);
	}	

	MsgSQLFinalize(stmt);
	MsgSQLClose(handle);

	// one and only one user - any other condition is 'bad'...
	if (users == 1) return 0;
	
	return 1;
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
	if (NULL == handle) return FALSE;
	
	stmt = MsgSQLPrepare (handle,
		"INSERT INTO users (username) VALUES (?);",
		&msgauth_stmts.add_user);
	
	if (MsgSQLBindString(stmt, 1, user, TRUE)) return FALSE;
	if (MsgSQLExecute(handle, stmt)) return FALSE;
	
	MsgSQLFinalize(stmt);
	MsgSQLClose(handle);
	
	return TRUE;
}

int
AuthSqlite_SetPassword(const char *user, const char *password)
{
	MsgSQLStatement *stmt;
	char path[XPL_MAX_PATH + 1];
	MsgSQLHandle *handle;
	
	AuthSqlite_GetDbPath(&path, XPL_MAX_PATH);
	handle = MsgSQLOpen(path, NULL, 1000);
	if (NULL == handle) return FALSE;
	
	stmt = MsgSQLPrepare (handle,
		"UPDATE users SET password = ? WHERE username= ?;",
		&msgauth_stmts.set_password);
	
	if (MsgSQLBindString(stmt, 1, password, TRUE)) return FALSE;
	if (MsgSQLBindString(stmt, 2, user, TRUE)) return FALSE;
	if (MsgSQLExecute(handle, stmt)) return FALSE;
	
	MsgSQLFinalize(stmt);
	MsgSQLClose(handle);
	
	return TRUE;
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
