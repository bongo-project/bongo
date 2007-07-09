/* Bongo Project licensing applies to this file, see COPYING
 * (C) 2007 Alex Hudson
 */

/** \file
 *  User authentication API
 */

#include <config.h>
#include <xpl.h>
#include <msgapi.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>

/**
 * Determine whether or not a user exists in our user database
 * SECURITY: We don't want to disclose this to unknown people
 * \param	user	The username to check
 * \return		Whether or not the user exists
 */
BOOL
MsgAuthFindUser(const char *user) {
	if (!strcmp(user, "admin"))
		return TRUE;
	
	return FALSE;
}

/**
 * Verify that the password we're given actually belongs to the user
 * \param	user	  User whose password we're checking
 * \param	password  The password we're giving
 * \return		  Whether or not the username matches the password
 */
BOOL
MsgAuthVerifyPassword(const char *user, const char *password) {
	if ((!strcmp(user, "admin")) && (!strcmp(password, "bongo")))
		return TRUE;
	
	return FALSE;
}

/**
 * Reset the user's password
 * \param	user	  	User whose password we're resetting
 * \param	oldpassword	Their old password
 * \param	newpassword	The new password we want to have
 * \return 			Whether or not we successfully changed the password
 */
BOOL
MsgAuthSetPassword(const char *user, const char *oldpassword, const char *newpassword) {
	return FALSE;
}

/**
 * Find the IP address of the user's store 
 * \param	user	  	User whose store we want
 * \param	store		Structure we can write the connection info to
 * \return 			Whether this was successful
 */
BOOL
MsgAuthGetUserStore(const char *user, struct sockaddr_in *store) {
	memset(store, 0, sizeof(store));
	store->sin_addr.s_addr = inet_addr("127.0.0.1");
	store->sin_family = AF_INET;
	store->sin_port = htons(689);
	return TRUE;
}
