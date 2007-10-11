/* Bongo Project licensing applies to this file, see COPYING
 * (C) 2007 Alex Hudson
 */

/** \file
 *  User authentication API
 */

#include <config.h>
#include <xpl.h>
#include <bongoutil.h>
#include <msgapi.h>

#define LOGGERNAME "msgauth"
#include <logger.h>

#include "plugin-api.h"

// returns 0 on success
int
MsgAuthLoadBackend(const char *name, const char *file)
{
	char path[XPL_MAX_PATH];
	XplPluginHandle *auth;
	char function[101];
	AuthPlugin_InterfaceVersion *version_func;
	MsgAuthAPIFunction *func;
	
	snprintf(path, XPL_MAX_PATH, "%s/bongo-auth/%s", XPL_DEFAULT_LIB_DIR, file);
	auth = XplLoadDLL(path);
	if (! auth) {
		Log(LOG_FATAL, "Unable to load auth backend '%s'", file);
		return 1;
	}
	
	func = &pluginapi[0];
	while (func->name) {
		snprintf(function, 100, "%s_%s", name, func->name);
		dlerror();
		func->func = dlsym(auth, function); 
		// XplGetDLLFunction("foo", function, auth);
		if (! dlerror()) {
			func->available = TRUE;
		} else {
			func->available = FALSE;
			if (! func->optional) {
				Log(LOG_ERROR, "Non-optional API call %s missing in backend %s",
					func->name, file);
				goto fail;
			}
		}
		func++;
	}
	
	func = &pluginapi[Func_InterfaceVersion];
	if (func->available) {
		int ver;
		version_func = (AuthPlugin_InterfaceVersion *)&func->func;
		ver = (*version_func)();
		if (ver >= 2) {
			// too new for us.
			Log(LOG_ERROR, "Auth backend %s implements version %d, we want %d",
				ver, 1);
			return 2;
		}
	}
	
	return 0;
	
fail:
	// XplUnloadDLL(auth);
	return 3;
}

int
MsgAuthInit(void)
{
	return MsgAuthLoadBackend("AuthSqlite", "libauthsqlite3.so");
}

/* returns 0 on success */
int
MsgAuthInitDB(void)
{
	return 0;
}

int
MsgAuthInstall(void)
{
	MsgAuthAPIFunction *function;
	AuthPlugin_Install *f;
	
	function = &pluginapi[Func_Install];
	if (! function->available) return 1;
	
	f = (AuthPlugin_Install *)&function->func;
	return (*f)();
}

/**
 * Determine whether or not a user exists in our user database
 * SECURITY: We don't want to disclose this to unknown people
 * Do we need this function? Often gets called before trying a
 * password, which is unnecessary - might be this can be removed.
 * This an MsgAuthVerifyPassword() are basically the same.
 * \param	user	The username to check
 * \return		Whether or not the user exists
 */
int
MsgAuthFindUser(const char *user)
{
	MsgAuthAPIFunction *function;
	AuthPlugin_FindUser *f;
	int result;
	
	function = &pluginapi[Func_FindUser];
	if (! function->available) return FALSE;
	
	f = (AuthPlugin_FindUser *)&function->func;
	result = (*f)(user);
	return result;
}

/**
 * Verify that the password we're given actually belongs to the user
 * \param	user	  User whose password we're checking
 * \param	password  The password we're giving
 * \return		  Whether or not the username matches the password
 */
int
MsgAuthVerifyPassword(const char *user, const char *password)
{
	MsgAuthAPIFunction *function;
	AuthPlugin_VerifyPassword *f;
	int result;
	
	function = &pluginapi[Func_VerifyPassword];
	if (! function->available) return FALSE;
	
	f = (AuthPlugin_VerifyPassword *)&function->func;
	result = (*f)(user, password);
    return result;
}

/* "Write" functions below */

/**
 * Add a user to the database. 
 * \param	user	Username to add
 * \return		True if the addition was successful
 */
int
MsgAuthAddUser(const char *user)
{
	MsgAuthAPIFunction *function;
	AuthPlugin_AddUser *f;
	int result;
	
	function = &pluginapi[Func_AddUser];
	if (! function->available) return FALSE;
	
	f = (AuthPlugin_AddUser *)&function->func;
	result = (*f)(user);
	return result;
}

/**
 * Change the user's password from an old one to a new one
 * \param	user	  	User whose password we're resetting
 * \param	oldpassword	Their old password
 * \param	newpassword	The new password we want to have
 * \return 			Whether or not we successfully changed the password
 */
int
MsgAuthChangePassword(const char *user, const char *oldpassword, const char *newpassword)
{
	MsgAuthAPIFunction *function;
	AuthPlugin_ChangePassword *f;
	int result;
	
	function = &pluginapi[Func_ChangePassword];
	if (function->available) {
		f = (AuthPlugin_ChangePassword *)&function->func;
		result = (*f)(user, oldpassword, newpassword);
		return result;
	} else {
		if (! MsgAuthVerifyPassword(user, oldpassword)) return FALSE;
		
		return MsgAuthSetPassword(user, newpassword);
	}
}

/**
 * Set the user's password. SECURITY: We should make sure we're 
 * authorised to do this.
 * \param	user	User whose password we're setting.
 * \param	password	New password we want to set
 * \return Whether or not the password got changed
 */
int
MsgAuthSetPassword(const char *user, const char *password)
{
	MsgAuthAPIFunction *function;
	AuthPlugin_SetPassword *f;
	int result;
	
	function = &pluginapi[Func_SetPassword];
	if (! function->available) return FALSE;
	
	f = (AuthPlugin_SetPassword *)&function->func;
	result = (*f)(user, password);
	return result;
}

/**
 * Find the IP address of the user's store 
 * \param	user	  	User whose store we want
 * \param	store		Structure we can write the connection info to
 * \return 			Whether this was successful
 */
int
MsgAuthGetUserStore(const char *user, struct sockaddr_in *store)
{
	MsgAuthAPIFunction *function;
	AuthPlugin_GetUserStore *f;
	int result;
	
	function = &pluginapi[Func_GetUserStore];
	if (function->available) {
		f = (AuthPlugin_GetUserStore *)&function->func;
		result = (*f)(user, store);
	} else {
		memset(store, 0, sizeof(store));
		store->sin_addr.s_addr = inet_addr("127.0.0.1");
		store->sin_family = AF_INET;
		store->sin_port = htons(689);
		result = 0;
	}
	return result;
}

/**
 * Get a complete list of usernames on the system
 * \param	list	A pointer we will set to the BongoArray of users
 * \return		Whether this was successful
 */
int
MsgAuthUserList(BongoArray **list)
{
	return -1;
}

