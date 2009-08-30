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

#ifndef MSGAPI_H
#define MSGAPI_H

#include <connio.h>
#include <sqlite3.h>
#include <xpl.h>

#include <msgftrs.h>
#include <msgdate.h>

// Auth functions
EXPORT int MsgAuthInit(void);
EXPORT int MsgAuthInstall(void);
EXPORT int MsgAuthFindUser(const char *user);
EXPORT BOOL MsgAuthVerifyPassword(const char *user, const char *password);
EXPORT int MsgAuthChangePassword(const char *user, const char *oldpassword, const char *newpassword);
EXPORT int MsgAuthSetPassword(const char *user, const char *password);
EXPORT int MsgAuthGetUserStore(const char *user, struct sockaddr_in *store);
EXPORT int MsgAuthInitDB(void);
EXPORT int MsgAuthAddUser(const char *user);
EXPORT int MsgAuthUserList(char **list[]);
EXPORT void MsgAuthUserListFree(char **list[]);

// Auth / cookie functions

#define MSGAUTH_COOKIE_LEN	32

typedef struct {
   	char 	 token[MSGAUTH_COOKIE_LEN];
	uint64_t expiration;
} MsgAuthCookie;

EXPORT BOOL MsgAuthCreateCookie(const char *username, MsgAuthCookie *cookie, uint64_t timeout);
EXPORT int  MsgAuthFindCookie(const char *username, const char *token);
EXPORT BOOL MsgAuthDeleteCookie(const char *username, const char *token);

// SQL Routines

typedef struct _MsgSQLStatement {
	sqlite3_stmt *stmt;
	void *userdata;
	const char *query;
} MsgSQLStatement;

typedef struct _MsgSQLHandle {
	sqlite3 *db;
	struct { 
		/* NOTE: begin and end must be the first and last stmts (see DStoreClose()) */
		MsgSQLStatement begin;
		MsgSQLStatement abort;
		MsgSQLStatement end;
	} stmts;

	BongoMemStack *memstack;
	XplMutex transactionLock;
	int transactionDepth;
	int lockTimeoutMs;
	BOOL isNew;		// have we just created this?
} MsgSQLHandle;

typedef enum {
	MSGAPI_DIR_START,
	MSGAPI_DIR_BIN,
	MSGAPI_DIR_CACHE,
	MSGAPI_DIR_CERT,
	MSGAPI_DIR_COOKIE,
	MSGAPI_DIR_DATA,
	MSGAPI_DIR_DBF,
	MSGAPI_DIR_LIB,
	MSGAPI_DIR_MAIL,
	MSGAPI_DIR_SCMS,
	MSGAPI_DIR_SPOOL,
	MSGAPI_DIR_STATE,
	MSGAPI_DIR_STORESYSTEM,
	MSGAPI_DIR_WORK,
	MSGAPI_DIR_END
} MsgApiDirectory;

typedef enum {
	MSGAPI_FILE_START,
	MSGAPI_FILE_PUBKEY,
	MSGAPI_FILE_PRIVKEY,
	MSGAPI_FILE_DHPARAMS,
	MSGAPI_FILE_RSAPARAMS,
	MSGAPI_FILE_RANDSEED,
	MSGAPI_FILE_END
} MsgApiFile;

EXPORT const char *
MsgGetFile(MsgApiFile file, char *buffer, size_t buffer_size);
EXPORT const char *
MsgGetDir(MsgApiDirectory directory, char *buffer, size_t buffer_size);

#define MSGSQL_STMT_SLEEP_MS 250

MsgSQLHandle *MsgSQLOpen(char *path, BongoMemStack *memstack, int locktimeoutms);
BongoMemStack *MsgSQLGetMemStack(MsgSQLHandle *handle);
MsgSQLStatement *MsgSQLPrepare(MsgSQLHandle *handle, const char *statement, MsgSQLStatement *stmt);
void 	MsgSQLClose(MsgSQLHandle *handle);
void 	MsgSQLSetMemStack(MsgSQLHandle *handle, BongoMemStack *memstack);
void 	MsgSQLSetLockTimeout(MsgSQLHandle *handle, int timeoutms);
void	MsgSQLReset(MsgSQLHandle *handle);
int	MsgSQLBeginTransaction(MsgSQLHandle *handle);
int	MsgSQLCommitTransaction(MsgSQLHandle *handle);
int	MsgSQLAbortTransaction(MsgSQLHandle *handle);
int	MsgSQLCancelTransactions(MsgSQLHandle *handle);

int	MsgSQLBindString(MsgSQLStatement *stmt, int var, const char *str, BOOL nullify);
int	MsgSQLBindInt(MsgSQLStatement *stmt, int var, int value);
int	MsgSQLBindInt64(MsgSQLStatement *stmt, int var, uint64_t value);
int	MsgSQLBindNull(MsgSQLStatement *stmt, int var);

int	MsgSQLResultInt(MsgSQLStatement *_stmt, int column);
uint64_t MsgSQLResultInt64(MsgSQLStatement *_stmt, int column);
int	MsgSQLResultText(MsgSQLStatement *_stmt, int column, char *result, size_t result_size);
int	MsgSQLResultTextPtr(MsgSQLStatement *_stmt, int column, char **ptr);
int	MsgSQLResults(MsgSQLHandle *handle, MsgSQLStatement *_stmt);

int	MsgSQLQuickExecute(MsgSQLHandle *handle, const char *query);
int	MsgSQLExecute(MsgSQLHandle *handle, MsgSQLStatement *_stmt);
void	MsgSQLFinalize(MsgSQLStatement *stmt);
void	MsgSQLEndStatement(MsgSQLStatement *_stmt);
int	MsgSQLStatementStep(MsgSQLHandle *handle, MsgSQLStatement *_stmt);
uint64_t MsgSQLLastRowID(MsgSQLHandle *handle);


// Misc. util functions

EXPORT BOOL MsgSetRecoveryFlag(char *agent_name);
EXPORT BOOL MsgGetRecoveryFlag(char *agent_name);
EXPORT BOOL MsgClearRecoveryFlag(char *agent_name);

EXPORT BOOL MsgGetServerCredential(char *buffer);
EXPORT BOOL MsgSetServerCredential(void);

#define MSGSRV_AGENT_ADDRESSBOOK            "Address Book Agent"
#define MSGSRV_AGENT_ALIAS                  "Alias Agent"
#define MSGSRV_AGENT_ANTISPAM               "AntiSpam Agent"
#define MSGSRV_AGENT_FINGER                 "Finger Agent"
#define MSGSRV_AGENT_GATEKEEPER             "Connection Manager"
#define MSGSRV_AGENT_CALCMD                 "Calendar Text Command Agent"
#define MSGSRV_AGENT_CONNMGR                "Connection Manager"
#define MSGSRV_AGENT_IMAP                   "IMAP Agent"
#define MSGSRV_AGENT_LIST                   "List Agent"
#define MSGSRV_AGENT_STORE                  "Store Agent"
#define MSGSRV_AGENT_QUEUE                  "Queue Agent"
#define MSGSRV_AGENT_POP                    "POP Agent"
#define MSGSRV_AGENT_PROXY                  "Proxy Agent"
#define MSGSRV_AGENT_RULESRV                "Rule Agent"
#define MSGSRV_AGENT_SIGNUP                 "Signup Agent"
#define MSGSRV_AGENT_SMTP                   "SMTP Agent"
#define MSGSRV_AGENT_ITIP                   "Itip Agent"
#define MSGSRV_AGENT_ANTIVIRUS              "AntiVirus Agent"
#define MSGSRV_AGENT_DMC                    "DMC Agent"
#define MSGSRV_AGENT_CMUSER                 "User Module"
#define MSGSRV_AGENT_CMLISTS                "Lists Module"
#define MSGSRV_AGENT_CMRBL                  "RBL Module"
#define MSGSRV_AGENT_CMRDNS                 "RDNS Module"
#define MSGSRV_AGENT_COLLECTOR              "Collector Agent"

/* Global configuration file.  See MsgGetConfigProperty() */
#define MSGSRV_CONFIG_FILENAME              "bongo.conf"
#define MSGSRV_CONFIG_MAX_PROP_CHARS         255
#define MSGSRV_CONFIG_PROP_MESSAGING_SERVER "MessagingServer"
#define MSGSRV_CONFIG_PROP_WEB_ADMIN_SERVER "WebAdminServer"
#define MSGSRV_CONFIG_PROP_DEFAULT_CONTEXT  "DefaultContext"
#define MSGSRV_CONFIG_PROP_BONGO_SERVICES    "BongoServices"
#define MSGSRV_CONFIG_PROP_MANAGED_SLAPD    "ManagedSlapd"
#define MSGSRV_CONFIG_PROP_MANAGED_SLAPD_PATH "ManagedSlapdPath"
#define MSGSRV_CONFIG_PROP_MANAGED_SLAPD_PORT "ManagedSlapdPort"
#define MSGSRV_CONFIG_PROP_MANAGED_SLAPD    "ManagedSlapd"

#define MSGFindUser(u, dn, t, n, v)         MsgFindObject((u), (dn), (t), (n), (v)) 

#ifdef __cplusplus
extern "C" {
#endif

EXPORT void MsgInit(void);
EXPORT BOOL MsgShutdown(void);

EXPORT const unsigned char *MsgGetServerDN(unsigned char *buffer);
EXPORT BOOL MsgGetConfigProperty(unsigned char *Buffer, unsigned char *Property);

EXPORT const unsigned char *MsgFindUserStore(const unsigned char *user, const unsigned char *defaultPath);
EXPORT BOOL MsgFindUserNmap(const unsigned char *user, unsigned char *nmap, int nmap_len, unsigned short *port);

EXPORT unsigned long MsgGetHostIPAddress(void);
EXPORT unsigned long MsgGetAgentBindIPAddress(void);

EXPORT const char *MsgGetUnprivilegedUser(void);

EXPORT void MsgMakePath(char *path);
EXPORT void MsgMakePathChown(char *path, int uid, int gid);
EXPORT BOOL MsgCleanPath(char *path);

EXPORT BOOL MsgResolveStart();
EXPORT BOOL MsgResolveStop();    

EXPORT void MsgGetUid(char *buffer, int buflen);

EXPORT BOOL MsgGetAvailableVersion(int *version);
EXPORT BOOL MsgGetBuildVersion(int *version, BOOL *custom);

EXPORT void MsgNmapChallenge(const unsigned char *response, unsigned char *reply, size_t length);

/* from msgcollector.c */

enum {
    MSG_COLLECT_OK,
    MSG_COLLECT_ERROR_URL = -1,
    MSG_COLLECT_ERROR_DOWNLOAD = -2,
    MSG_COLLECT_ERROR_AUTH = -3,
    MSG_COLLECT_ERROR_PARSE = -4,
    MSG_COLLECT_ERROR_STORE = -5,
};  

EXPORT int MsgCollectUser(const char *user);
EXPORT void MsgCollectAllUsers(void);
EXPORT int MsgIcsImport(const char *user, 
                        const char *calendarName, 
                        const char *calendarColor, 
                        const char *url, 
                        const char *urlUsername, 
                        const char *urlPassword, 
                        uint64_t *guidOut);

EXPORT int MsgIcsSubscribe(const char *user, 
                           const char *calendarName, 
                           const char *calendarColor, 
                           const char *url, 
                           const char *urlUsername, 
                           const char *urlPassword, 
                           uint64_t *guidOut);

EXPORT int MsgImportIcs(FILE *fh, 
                        const char *user, 
                        const char *calendarName, 
                        const char *calendarColor, 
                        Connection *existingConn);

EXPORT int MsgImportIcsUrl(const char *user, 
                           const char *calendarName, 
                           const char *calendarColor, 
                           const char *url, 
                           const char *urlUsername, 
                           const char *urlPassword, 
                           Connection *existingConn);

#ifdef __cplusplus
}
#endif

#endif /* MSGAPI_H */

