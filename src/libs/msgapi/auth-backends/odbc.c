/* Bongo Project licensing applies to this file, see COPYING
 */

 /** \file
  *  ODBC backend for user authentication
  */

#include <stdio.h>
#include <config.h>
#include <xpl.h>
#include <bongoutil.h>
#include <msgapi.h>
#define LOGGERNAME "msgauth-odbc-backend"
#include <logger.h>
 
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <time.h>

#include <sql.h>
#include <sqlext.h>

/** Bongo msgapi compatibility version */
#define BONGO_AUTH_PLUGIN_VERSION 1

/** default connection string prototype */
#define CONNECTION_STRING_PROTOTYPE	"DRIVER={SQLite3};DATABASE=%s;"
#define CONNECTION_STRING_PROTOTYPE_LEN	strlen(CONNECTION_STRING_PROTOTYPE)

/** default connection string lenght */
#define CONNECTION_STRING_LEN	(XPL_MAX_PATH + CONNECTION_STRING_PROTOTYPE_LEN +1)

/* SQL prototypes */

/** Verify User and password sql query prototype */
#define SQL_STATEMENT_AUTH_USER	"SELECT count(username) FROM users WHERE username = ? AND password = ?;"

/** Find User sql query prototype */
#define SQL_STATEMENT_FIND_USER "SELECT count(username) FROM users WHERE username = ?;"

/** Add User sql query prototype */
#define SQL_STATEMENT_ADD_USER "INSERT INTO users (username) VALUES (?);"

/** Set User password sql query prototype */
#define SQL_STATEMENT_SET_PASSWORD "UPDATE users SET password = ? WHERE username= ?;"

/** Definitions of the Database */
typedef struct _ODBCAuthDatabase{
	char *connection_string;
	char *sql_auth_user;
	char *sql_find_user;
	char *sql_add_user;
	char *sql_set_password;
}ODBCAuthDatabase;

ODBCAuthDatabase DatabaseDefine;

/* Auxiliary Function Prototypes */
void AuthODBC_ReturnErrorMessage(int retcode, const char *Message);
int AuthODBC_GetDbPath(char *path, size_t size);
int AuthODBC_GenerateHash(const char *username, const char *password, char *result, size_t result_len);

/* backend functions */
int AuthODBC_Init(void);
int AuthODBC_Install(void);
int AuthODBC_FindUser(const char *user);
int AuthODBC_VerifyPassword(const char *user, const char *password);
int AuthODBC_AddUser(const char *user);
int AuthODBC_SetPassword(const char *user, const char *password);
int AuthODBC_UserList(BongoArray **list);
int AuthODBC_InterfaceVersion(void);

/** Backend initiation function.
 *  It reads the configuration and sets the database definitions.
 * \return 0 if it has no errors.
 */
int
AuthODBC_Init(void)
{
	char path[XPL_MAX_PATH + 1];
	Log(LOG_DEBUG,"ODBC Auth Plugin: Init backend");

	/* Build the default connection string */
	AuthODBC_GetDbPath(path, XPL_MAX_PATH + 1);
	DatabaseDefine.connection_string=malloc(CONNECTION_STRING_LEN*sizeof(char));
	snprintf(DatabaseDefine.connection_string, CONNECTION_STRING_LEN, CONNECTION_STRING_PROTOTYPE , path);
	//DatabaseDefine.connection_string = "DRIVER={PostgreSQL Unicode};SERVER=localhost;DATABASE=bongo;UID=gass;PWD=12345;PORT=5432;";

	/* Define auth_user sql query */
	DatabaseDefine.sql_auth_user=SQL_STATEMENT_AUTH_USER;

	/* Define find_user sql query */
	DatabaseDefine.sql_find_user=SQL_STATEMENT_FIND_USER;

	/* Define add_user sql query */
	DatabaseDefine.sql_add_user=SQL_STATEMENT_ADD_USER;

	/* Define set_password sql query */
	DatabaseDefine.sql_set_password=SQL_STATEMENT_SET_PASSWORD;

	return 0;	
}


/* returns 0 on success */
int
AuthODBC_Install(void)
{
	/* removed all sqlite stuff */	
	return 0;
}

int
AuthODBC_FindUser(const char *user)
{
	SQLHENV env;
	SQLHDBC handle;
	SQLHSTMT stmt;
	SQLSMALLINT users[1];
	SQLLEN users_len[1];
	SQLINTEGER user_buffer;
	SQLSMALLINT * OutConnStrLen;
	SQLCHAR OutConnectionString[1025];
	int retcode;

	Log(LOG_DEBUG,"ODBC Auth Plugin: Entered Find User");
	
	/* Set Environment and Connect */
	Log(LOG_DEBUG, "connection string: %s", DatabaseDefine.connection_string);
	/* Set the environment */
	retcode = SQLAllocHandle(SQL_HANDLE_ENV, SQL_NULL_HANDLE, &env);
	SQLSetEnvAttr(env, SQL_ATTR_ODBC_VERSION, (SQLPOINTER*)SQL_OV_ODBC3,0);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - SqlAllocHandle ENV");
	
	/* connection */
	retcode = SQLAllocHandle(SQL_HANDLE_DBC, env, &handle);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - SqlAllocHandle ODBC HANDLE");	
	
	/* Connect to the database */
	retcode = SQLDriverConnect(handle, NULL,DatabaseDefine.connection_string,
				    SQL_NTS, OutConnectionString, 1024, OutConnStrLen,
				    SQL_DRIVER_COMPLETE);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - ODBC Connection");
	if (retcode != SQL_SUCCESS && retcode != SQL_SUCCESS_WITH_INFO) {
		retcode = SQLFreeHandle(SQL_HANDLE_DBC, handle);
		AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC handle");
		retcode = SQLFreeHandle(SQL_HANDLE_ENV, env);
		AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC environment");
		return 2;
	}

	/* Prepare SQL Query */
	retcode = SQLAllocHandle(SQL_HANDLE_STMT, handle, &stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - SqlAllocHandle stmt");

	Log(LOG_DEBUG, "Find_User - SQL statement: %s", DatabaseDefine.sql_find_user);
	retcode = SQLPrepare (stmt, (SQLCHAR*) DatabaseDefine.sql_find_user, SQL_NTS);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - Sql Query preparation");	

	user_buffer=SQL_NTS;
	retcode = SQLBindParameter(stmt, 1, SQL_PARAM_INPUT, SQL_C_CHAR, SQL_VARCHAR, 100, 0, 
				   (SQLPOINTER) user,0, &user_buffer);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - SqlBindParameter user");

	users_len[0]=1;
	retcode = SQLBindCol(stmt, 1, SQL_C_SSHORT, users, 1, users_len);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - SqlBindCol user");
	
	/* Query the Database */
	retcode = SQLExecute(stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - SqlExecute");
	retcode = SQLFetch(stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - SqlFetch");
	SQLCancel(stmt);
	
	/* Closing and freeing */
	retcode = SQLFreeHandle(SQL_HANDLE_STMT, stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - Free ODBC stmt");
	retcode = SQLDisconnect(handle);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - ODBC Disconnect");
	retcode = SQLFreeHandle(SQL_HANDLE_DBC, handle);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - Free ODBC handle");
	retcode = SQLFreeHandle(SQL_HANDLE_ENV, env);
	AuthODBC_ReturnErrorMessage(retcode, "Find_User - Free ODBC environment");

	/* Processing OutPut */
	if (!(retcode == SQL_SUCCESS || retcode == SQL_SUCCESS_WITH_INFO)) {
		return 2;
        }	
	Log(LOG_DEBUG, "Find_User - Number of users %d", users[0]);
	// one and only one user - any other condition is 'bad'...
	if (users[0] == 1) {
		return 0; // user exists / password verified
	}
	if (users[0] == 0) {
		Log(LOG_ERROR, "Find_User - User not in db", user);
		return 1; // no such user or password wrong, not an error
	}
	
	Log(LOG_ERROR, "Find_User - User %s exists multiple times in the user db", user);
	return 3;
}

int
AuthODBC_VerifyPassword(const char *user, const char *password)
{
	SQLHENV env;
	SQLHDBC handle;
	SQLHSTMT stmt;
	SQLSMALLINT users[1];
	SQLLEN users_len[1];
	SQLINTEGER user_buffer;
	char hash[XPLHASH_SHA1_LENGTH + 1];
	SQLSMALLINT * OutConnStrLen;
	SQLCHAR OutConnectionString[1025];
	int retcode;

	Log(LOG_DEBUG,"ODBC Auth Plugin: Verify Password");
	
	if (AuthODBC_GenerateHash(user, password, hash, XPLHASH_SHA1_LENGTH + 1) != 0) 
		return 1;
	
	/* Set Environment and Connect */
	Log(LOG_DEBUG, "Verify_password - connection string: %s", DatabaseDefine.connection_string);
	/* Set the environment */
	retcode = SQLAllocHandle(SQL_HANDLE_ENV, SQL_NULL_HANDLE, &env);
	SQLSetEnvAttr(env, SQL_ATTR_ODBC_VERSION, (SQLPOINTER*)SQL_OV_ODBC3,0);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - SqlAllocHandle ENV");
	
	/* connection */
	retcode = SQLAllocHandle(SQL_HANDLE_DBC, env, &handle);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - SqlAllocHandle ODBC HANDLE");	
	
	/* Connect to the database */
	retcode = SQLDriverConnect(handle, NULL,DatabaseDefine.connection_string,
				    SQL_NTS, OutConnectionString, 1024, OutConnStrLen,
				    SQL_DRIVER_COMPLETE);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - ODBC Connection");
	if (retcode != SQL_SUCCESS && retcode != SQL_SUCCESS_WITH_INFO) {
		retcode = SQLFreeHandle(SQL_HANDLE_DBC, handle);
		AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC handle");
		retcode = SQLFreeHandle(SQL_HANDLE_ENV, env);
		AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC environment");
		return 2;
	}

	/* Prepare SQL Query */
	retcode = SQLAllocHandle(SQL_HANDLE_STMT, handle, &stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - SqlAllocHandle stmt");

	Log(LOG_DEBUG, "Verify_password - SQL statement: %s", DatabaseDefine.sql_auth_user);
	retcode = SQLPrepare (stmt, (SQLCHAR*) DatabaseDefine.sql_auth_user, SQL_NTS);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Sql Query preparation");	

	user_buffer=SQL_NTS;
	retcode = SQLBindParameter(stmt, 1, SQL_PARAM_INPUT, SQL_C_CHAR, SQL_VARCHAR, 100, 0, 
				   (SQLPOINTER) user,0, &user_buffer);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - SqlBindParameter user");

	user_buffer = SQL_NTS;
	retcode = SQLBindParameter(stmt, 2, SQL_PARAM_INPUT, SQL_C_CHAR, SQL_VARCHAR, 
				   XPLHASH_SHA1_LENGTH, 0, (SQLPOINTER) hash,0,  &user_buffer);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - SqlBindParameter hash");
	
	users_len[0]=1;
	retcode = SQLBindCol(stmt, 1, SQL_C_SSHORT, users, 1, users_len);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - SqlBindCol user");
	
	/* Query the Database */
	retcode = SQLExecute(stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - SqlExecute");
	retcode = SQLFetch(stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - SqlFetch");
	SQLCancel(stmt);
	
	/* Closing and freeing */
	retcode = SQLFreeHandle(SQL_HANDLE_STMT, stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC stmt");
	retcode = SQLDisconnect(handle);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - ODBC Disconnect");
	retcode = SQLFreeHandle(SQL_HANDLE_DBC, handle);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC handle");
	retcode = SQLFreeHandle(SQL_HANDLE_ENV, env);
	AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC environment");
	
	/* Processing OutPut */
	if (retcode != SQL_SUCCESS && retcode != SQL_SUCCESS_WITH_INFO){
		return 2;
	}

	Log(LOG_DEBUG, "Verify_Password - Number of users %d", users[0]);
	// one and only one user - any other condition is 'bad'...
	if (users[0] == 1) {
		return 0; // user exists / password verified
	}
	if (users[0] == 0) {
		Log(LOG_ERROR, "User not in db", user);
		return 1; // no such user or password wrong, not an error
	}
	
	Log(LOG_ERROR, "User %s exists multiple times in the user db", user);
	return 3;
}

/* "Write" functions below */

int
AuthODBC_AddUser(const char *user)
{
	SQLHENV env;
	SQLHDBC handle;
	SQLHSTMT stmt;
	SQLINTEGER user_buffer;
	SQLSMALLINT * OutConnStrLen;
	SQLCHAR OutConnectionString[1025];
	int retcode;

	Log(LOG_DEBUG,"ODBC Auth Plugin: Entered Add User");
	
	/* Set Environment and Connect */
	Log(LOG_DEBUG, "Add_User - connection string: %s", DatabaseDefine.connection_string);
	/* Set the environment */
	retcode = SQLAllocHandle(SQL_HANDLE_ENV, SQL_NULL_HANDLE, &env);
	SQLSetEnvAttr(env, SQL_ATTR_ODBC_VERSION, (SQLPOINTER*)SQL_OV_ODBC3,0);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - SqlAllocHandle ENV");
	
	/* connection */
	retcode = SQLAllocHandle(SQL_HANDLE_DBC, env, &handle);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - SqlAllocHandle ODBC HANDLE");	
	
	/* Connect to the database */
	retcode = SQLDriverConnect(handle, NULL,DatabaseDefine.connection_string,
				    SQL_NTS, OutConnectionString, 1024, OutConnStrLen,
				    SQL_DRIVER_COMPLETE);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - ODBC Connection");

	if (retcode != SQL_SUCCESS && retcode != SQL_SUCCESS_WITH_INFO) {
		retcode = SQLFreeHandle(SQL_HANDLE_DBC, handle);
		AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC handle");
		retcode = SQLFreeHandle(SQL_HANDLE_ENV, env);
		AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC environment");
		return 2;
	}

	/* Prepare SQL Query */
	retcode = SQLAllocHandle(SQL_HANDLE_STMT, handle, &stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - SqlAllocHandle stmt");


	retcode = SQLPrepare (stmt, (SQLCHAR*) DatabaseDefine.sql_add_user, SQL_NTS);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - Sql Query preparation");	

	user_buffer=SQL_NTS;
	retcode = SQLBindParameter(stmt, 1, SQL_PARAM_INPUT, SQL_C_CHAR, SQL_VARCHAR, 100, 0, 
				   (SQLPOINTER) user,0, &user_buffer);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - SqlBindParameter user");

	/* Query the Database */
	retcode = SQLExecute(stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - SqlExecute");
	
	SQLCancel(stmt);
	
	/* Closing and freeing */
	retcode = SQLFreeHandle(SQL_HANDLE_STMT, stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - Free ODBC stmt");
	retcode = SQLDisconnect(handle);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - ODBC Disconnect");
	retcode = SQLFreeHandle(SQL_HANDLE_DBC, handle);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - Free ODBC handle");
	retcode = SQLFreeHandle(SQL_HANDLE_ENV, env);
	AuthODBC_ReturnErrorMessage(retcode, "Add_User - Free ODBC environment");
	if (retcode != SQL_SUCCESS && retcode != SQL_SUCCESS_WITH_INFO){
		return 2;
	}

	return 0;
}

int
AuthODBC_SetPassword(const char *user, const char *password)
{
	SQLHENV env;
	SQLHDBC handle;
	SQLHSTMT stmt;
	SQLINTEGER user_buffer;
	char hash[XPLHASH_SHA1_LENGTH + 1];
	SQLSMALLINT * OutConnStrLen;
	SQLCHAR OutConnectionString[1025];
	int retcode;

	Log(LOG_DEBUG,"ODBC Auth Plugin: Entered Set Password");
		
	if (AuthODBC_GenerateHash(user, password, hash, XPLHASH_SHA1_LENGTH + 1) != 0) 
		return 1;
	
	/* Set Environment and Connect */
	Log(LOG_DEBUG, "Set_Password - connection string: %s", DatabaseDefine.connection_string);
	/* Set the environment */
	retcode = SQLAllocHandle(SQL_HANDLE_ENV, SQL_NULL_HANDLE, &env);
	SQLSetEnvAttr(env, SQL_ATTR_ODBC_VERSION, (SQLPOINTER*)SQL_OV_ODBC3,0);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - SqlAllocHandle ENV");
	
	/* connection */
	retcode = SQLAllocHandle(SQL_HANDLE_DBC, env, &handle);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - SqlAllocHandle ODBC HANDLE");	
	
	/* Connect to the database */
	retcode = SQLDriverConnect(handle, NULL,DatabaseDefine.connection_string,
				    SQL_NTS, OutConnectionString, 1024, OutConnStrLen,
				    SQL_DRIVER_COMPLETE);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - ODBC Connection");

	if (retcode != SQL_SUCCESS && retcode != SQL_SUCCESS_WITH_INFO) {
		retcode = SQLFreeHandle(SQL_HANDLE_DBC, handle);
		AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC handle");
		retcode = SQLFreeHandle(SQL_HANDLE_ENV, env);
		AuthODBC_ReturnErrorMessage(retcode, "Verify_password - Free ODBC environment");
		return 2;
	}

	/* Prepare SQL Query */
	retcode = SQLAllocHandle(SQL_HANDLE_STMT, handle, &stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - SqlAllocHandle stmt");

	retcode = SQLPrepare (stmt, (SQLCHAR*) DatabaseDefine.sql_set_password, SQL_NTS);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - Sql Query preparation");	

	user_buffer=SQL_NTS;
	retcode = SQLBindParameter(stmt, 2, SQL_PARAM_INPUT, SQL_C_CHAR, SQL_VARCHAR, 100, 0, 
				   (SQLPOINTER) user,0, &user_buffer);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - SqlBindParameter user");

	user_buffer = SQL_NTS;
	retcode = SQLBindParameter(stmt, 1, SQL_PARAM_INPUT, SQL_C_CHAR, SQL_VARCHAR, 
				   XPLHASH_SHA1_LENGTH, 0, (SQLPOINTER) &hash,0,  &user_buffer);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - SqlBindParameter hash");
	
	/* Query the Database */
	retcode = SQLExecute(stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - SqlExecute");
	SQLCancel(stmt);
	
	/* Closing and freeing */

	retcode = SQLFreeHandle(SQL_HANDLE_STMT, stmt);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - Free ODBC stmt");
	retcode = SQLDisconnect(handle);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - ODBC Disconnect");
	retcode = SQLFreeHandle(SQL_HANDLE_DBC, handle);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - Free ODBC handle");
	retcode = SQLFreeHandle(SQL_HANDLE_ENV, env);
	AuthODBC_ReturnErrorMessage(retcode, "Set_Password - Free ODBC environment");
	
	if (retcode != SQL_SUCCESS && retcode != SQL_SUCCESS_WITH_INFO) {
		return 2;
        }	
	return 0;
}

int
AuthODBC_UserList(BongoArray **list)
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
AuthODBC_InterfaceVersion(void)
{
	return BONGO_AUTH_PLUGIN_VERSION;
}

/** Logs the error corresponding to the submitted error code.
 * \param retcode error code from the odbc function.
 * \param Message message shown before the error message.
 *	  example: "Connection status" for the log "Connection status: SQL_SUCCESS.
 */
void
AuthODBC_ReturnErrorMessage(int retcode, const char *Message)
{
	switch (retcode)
	{
		case SQL_SUCCESS:
			Log(LOG_DEBUG,"%s: SQL_SUCCESS", Message);
			break;
		case SQL_SUCCESS_WITH_INFO:
			Log(LOG_DEBUG,"%s: SQL_SUCCESS_WITH_INFO", Message);
			break;
		case SQL_NO_DATA:
			Log(LOG_ERROR,"%s: SQL_NO_DATA", Message);
			break;
		case SQL_ERROR:
			Log(LOG_ERROR,"%s: SQL_ERROR", Message);
			break;
		case SQL_INVALID_HANDLE:
			Log(LOG_ERROR,"%s: SQL_INVALID_HANDLE", Message);
			break;
		default:
			return;
	}
}
int
AuthODBC_GetDbPath(char *path, size_t size)
{
	return snprintf(path, size, "%s/%s", XPL_DEFAULT_DBF_DIR, "userdb.sqlite");
}

int
AuthODBC_GenerateHash(const char *username, const char *password, char *result, size_t result_len)
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
int
main (int argc, char *argv[])
{
	printf ("############ Bongo Project ############\n");
	printf ("      Bongo MsgApiAuth plugin          \n");
	printf ("       ODBC Support Plugin             \n");
	printf ("      Compatibility Version: %d        \n", BONGO_AUTH_PLUGIN_VERSION);
	return 0;
}
