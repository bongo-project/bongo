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
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <time.h>

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

/**
 * Get a complete list of usernames on the system
 * \param	list	A pointer we will set to the BongoArray of users
 * \return		Whether this was successful
 */
BOOL
MsgAuthUserList(BongoArray **list) {
	BongoArray *userlist;

	userlist = BongoArrayNew(sizeof (char *), 0);

	char *admin = strdup("admin");
	BongoArrayAppendValue(userlist, admin);

	*list = userlist;
	return TRUE;
}

/* Cookie stuff. Cookies are managed by msgapi, so that all agents that know about 
 * cookies will know which cookies are valid and which aren't.
 */

void
MsgAuthCookiePath(const char *username, char *path, size_t length)
{
	snprintf(path, length, "%s/cookies/%s", XPL_DEFAULT_DBF_DIR, username);
}

/**
 * Create a 'cookie': a password-substitute token, and an associated expiry 
 * SECURITY: We shouldn't create cookies for non-authenticated users
 * \param	user	User whose token this will be
 * \param	cookie	Reference to the MsgAuthCookie we're creating
 * \param	timeout	When the token should expire
 * \return		Whether or not the cookie was created
 */
EXPORT BOOL
MsgAuthCreateCookie(const char *username, MsgAuthCookie *cookie, uint64_t timeout)
{
	char path[XPL_MAX_PATH+1];
	FILE *cookiefile = NULL;
	long original_length;
	size_t written;
	xpl_hash_context context;

	// create the cookie
	cookie->expiration = timeout;
	
	XplHashNew(&context, XPLHASH_MD5);
	snprintf(cookie->token, sizeof(cookie->token), "%x%x", 
			(unsigned int) XplGetThreadID(), 
			(unsigned int) time(NULL));
	XplHashWrite(&context, cookie->token, strlen(cookie->token));
	XplRandomData(cookie->token, 8);
	XplHashWrite(&context, cookie->token, strlen(cookie->token));
	XplHashFinal(&context, XPLHASH_LOWERCASE, cookie->token, XPLHASH_MD5_LENGTH);

	// save it to our cookie list
	MsgAuthCookiePath(username, &path, XPL_MAX_PATH);
	cookiefile = fopen(path, "a");
	if (! cookiefile)
		return FALSE;

	original_length = ftell(cookiefile);

	written = fwrite(cookie, 1, sizeof(MsgAuthCookie), cookiefile);
	if ((fclose(cookiefile) != 0) || (sizeof(MsgAuthCookie) != written)) {
		truncate(path, original_length);
		return FALSE;
	}

	return TRUE;
}

/**
 * Find a cookie based on the token. If it exists, it's as good as auth'ing
 * SECURITY: This is much like authentication. Be careful to timeout on bad auth?
 * \param	username	User who claims this token is theirs
 * \param 	token		Token we want to look for
 * \return	0 = bad auth, 1 = good auth, 2 = internal error
 */
EXPORT int
MsgAuthFindCookie(const char *username, const char *token)
{
	char path[XPL_MAX_PATH+1];
	FILE *cookiefile = NULL;
	MsgAuthCookie cookie;
	int result = 1;
	BOOL expired_tokens = FALSE;
	uint64_t now;

	now = time(NULL);

	MsgAuthCookiePath(username, &path, XPL_MAX_PATH);

	cookiefile = fopen(path, "r");
	if (! cookiefile) return 2;

	while (fread(&cookie, 1, sizeof(MsgAuthCookie), cookiefile) != sizeof(MsgAuthCookie)) {
		if (cookie.expiration < now) {
			expired_tokens = TRUE;
			continue;
		}
		if (!strncmp (cookie.token, token, MSGAUTH_COOKIE_LEN)) {
			result = 1;
			break;
		}
	}

	if (ferror(cookiefile))
		result = 2;

	fclose(cookiefile);

	if (expired_tokens)
		// call this function with no token to remove expired tokens
		MsgAuthDeleteCookie(username, "");

	return result;
}

/**
 * Remove a token from the user's cookie store. Removes expired tokens as a side-effect.
 * \param	username	User whose token this should be
 * \param	token		Token we want rid of.
 * \return	Whether or not we succeeded.
 */
EXPORT BOOL
MsgAuthDeleteCookie(const char *username, const char *token)
{
	char path[XPL_MAX_PATH + 1];
	FILE *cookiefile = NULL;
	long filelen;
	int cookiecount, i;
	MsgAuthCookie **cookieset;
	uint64_t now;

	now = BongoCalTimeAsUint64(BongoCalTimeNow(NULL));

	MsgAuthCookiePath(username, &path, XPL_MAX_PATH);

	// first, read cookies into memory 
	cookiefile = fopen(path, "w");
	if (! cookiefile) return FALSE;

	if (fseek(cookiefile, 0, SEEK_END) != 0) {
		fclose(cookiefile);
		return FALSE;
	}

	filelen = ftell(cookiefile);
	cookiecount = filelen / sizeof(MsgAuthCookie);
	cookieset = MemMalloc(filelen + 1);
	if (fread(cookieset, 1, filelen, cookiefile) != filelen) {
		fclose(cookiefile);
		return FALSE;
	}

	// next, clear the existing file
	rewind(cookiefile);
	ftruncate((int)cookiefile, 0);

	// now, go through memory writing out only the cookies we want to keep
	for (i = 0; i < cookiecount; i++) {
		MsgAuthCookie *cookie = cookieset[i];
		if (! strncmp(cookie->token, token, MSGAUTH_COOKIE_LEN)) {
			// we want to delete this token
			cookie->expiration = 0;
		}
		if (cookie->expiration < now) {
			// this token is expired, so we don't want this.
			cookie->expiration = 0;
		}
		if (cookie->expiration != 0) {
			// we want to keep this token, so write it out
			size_t written;
			written = fwrite(cookie, 1, sizeof(MsgAuthCookie), cookiefile);
			if (written != sizeof(MsgAuthCookie)) {
				// ooops, this is pretty bad...
				fclose(cookiefile);
				return FALSE;
			}
		}
	}

	fclose(cookiefile);
	return TRUE;
}
