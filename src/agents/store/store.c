/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005, 2006 Novell, Inc. All Rights Reserved.
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
// Parts Copyright (C) 2007 Alex Hudson. See COPYING for details.

#include <config.h>
#include <xpl.h>
#include <bongoutil.h>
#include <assert.h>
#include <limits.h>
#include <time.h>

#include "stored.h"

#include "messages.h"
#include "mime.h"
#include "mail.h"
#include "calendar.h"

int
IsOwnStoreSelected(StoreClient *client)
{
    return (STORE_PRINCIPAL_USER == client->principal.type && 
            (!strcmp(client->principal.name, client->storeName)));
}

/* Creates a new collection, including any ancestors required.  Also touches the file
   for this collection (but doesn't truncate it, since this function is also used by
   the RENAME command (which is a bit of an abstraction violation))

   returns:  0 success
            -1 bad collection name
            -2 db lib err
            -3 guid exists
            -4 io error
            -5 permission denied
*/

int
StoreCreateCollection(StoreClient *client, char *name, uint64_t guid,
                      uint64_t *outGuid, int32_t *outVersion)
{
    StoreObject new_collection;
    char *p;
    int res;
    int namelen = strlen(name);

    /* validate collection name */
    if ('/' != *name ||
        namelen < 2 ||
        namelen > STORE_MAX_COLLECTION - 1 ||
        '/' == name[namelen-1])
    {
        return -1;
    }

    memset (&new_collection, 0, sizeof(StoreObject));
    new_collection.type = STORE_DOCTYPE_FOLDER;
    new_collection.collection_guid = 1; /* root guid */
    
	if (guid)
		new_collection.guid = guid;	// check at some point?

    p = name;
    do {
    	// FIXME: recursively create any collections in our path which don't exist yet
    	StoreObject collection;
    	collection.guid = 0;

/*    	
    	collection.guid = 0;
        *p++ = '/';
        p = strchr(p, '/');
        if (p)
        	*p = 0;
        else
        	collection.guid = guid;
        
           
        dcode = DStoreFindCollectionGuid(client->handle, name, &info.collection);
        if (-1 == dcode) {
            return -2;
        } else if (1 == dcode) {
            if (!p) {
                return -3;
            } else {
                continue;
            }
        }

        if (StoreCheckAuthorizationGuidQuiet(client, info.collection, 
                                             STORE_PRIV_BIND)) 
        {
            return -5;
        }

        strncpy (info.filename, name, sizeof(info.filename) - 1);

        if (DStoreSetDocInfo(client->handle, &info)) {
            return -2;
        }
        info.collection = info.guid;
*/
        res = MaildirNew(client->store, collection.guid);
        if (res != 0) return res;
    } while (p);

    if (outGuid) *outGuid = new_collection.guid;
    if (outVersion) *outVersion = new_collection.version;
    
    return 0;
}
                 
/* find the user's store path, creating it if necessary 
   path must be a buffer of size XPL_MAX_PATH + 1 or greater
   returns 0 on success, -1 o/w
*/

int
SetupStore(const char *user, const char **storeRoot, char *path, size_t len)
{
	int n;
	struct stat sb;
	BOOL make_store = FALSE;

	n = snprintf(path, len, "%s/%s/", StoreAgent.store.rootDir, user);
	path[len] = 0;
	path[n-1] = 0;

	if (stat(path, &sb)) {
		if (XplMakeDir(path)) {
			Log(LOG_ERROR, "Error creating store directory: %s.", strerror(errno));
			return -1;
		}
		make_store = TRUE;
	}
	
	path[n-1] = '/';

	if (make_store) {
		uint64_t i;
		for (i = 0x2; i < 0xe; i++) {
			if (MaildirNew(path, i)) {
				Log(LOG_ERROR, "Error creating store: %s.", strerror(errno));
				return -1;
			}
		}
	}
	path[n] = 0;

	*storeRoot = NULL;

	return 0;
}


/* This function gets called after a document has been added/replaced to the store,
   before committing the transaction

   NOTE: info->data fields are not valid after this function has been called.
*/

const char *
StoreProcessDocument(StoreClient *client, 
                     StoreObject *document,
                     const char *path)
{
    const char *result = NULL;
    void *mark = BongoMemStackPeek(&client->memstack);

    /* type-dependent processing */

    switch (document->type) {
    case STORE_DOCTYPE_MAIL:
        result = StoreProcessIncomingMail(client, document, path);
        break;
    case STORE_DOCTYPE_EVENT:
        // FIXME: how do we link this into a calendar automatically?
        result = StoreProcessIncomingEvent(client, document, 0);
        break;
    default:
        break;
    }

    BongoMemStackPop(&client->memstack, mark);

    return result;
}

/* deliver message to a mailbox 
   msgfile must be an open file whose file pointer set to the start of the file.

   Implementation: this delivers mail straight into the store. Previously it wrote to
   an 'incoming' file, leading to long wait selecting a store when lots of mail is waiting.
*/

// can return: DELIVER_QUOTA_EXCEEDED  DELIVER_FAILURE DELIVER_TRY_LATER
NMAPDeliveryCode
DeliverMessageToMailbox(StoreClient *client,
                        char *sender, char *authSender, char *recipient, char *mbox,
                        FILE *msgfile, unsigned long scmsID, size_t msglen, 
                        unsigned long flags)
{
	StoreObject collection;
	StoreObject newmail;
	FILE *tmp = NULL;
	const char *storeRoot;
	char tmppath[XPL_MAX_PATH+1];
	char path[XPL_MAX_PATH+1];
	char mailbox[XPL_MAX_PATH+1];
	unsigned char timeBuffer[80];
	char buffer[1024];

	if (scmsID || msglen) {
		// FIXME : SCMS isn't implemented this way any more.
		// can we get rid of msglen also?
	}

	if (SetupStore(recipient, &storeRoot, path, sizeof(path))) {
		return DELIVER_TRY_LATER;
	}

	memset(&newmail, 0, sizeof(StoreObject));
	
	if (mbox == NULL)
		mbox = "INBOX";
	
	snprintf(mailbox, XPL_MAX_PATH, "/mail/%s", mbox);
	mailbox[XPL_MAX_PATH] = '\0';
	
	if (StoreObjectFindByFilename(client, mailbox, &collection)) {
		// couldn't find the wanted folder;
		return DELIVER_FAILURE;
	}
	
	MaildirTempDocument(client, collection.guid, tmppath, sizeof(tmppath));
	tmp = fopen(tmppath, "w");
	if (tmp == NULL)
		return DELIVER_TRY_LATER;
	
	// write out the mail headers
	if (authSender && authSender[0] != '-') {
		fprintf(tmp, "X-Auth-OK: %s\r\n", authSender);
	} else {
		fprintf(tmp, "X-Auth-No: \r\n");
	}
	
	MsgGetRFC822Date(-1, 0, timeBuffer);
	fprintf(tmp, "Received: from %d.%d.%d.%d [%d.%d.%d.%d] by %s\r\n\twith Store (%s); %s\r\n",
		client->conn->socketAddress.sin_addr.s_net,
		client->conn->socketAddress.sin_addr.s_host,
		client->conn->socketAddress.sin_addr.s_lh,
		client->conn->socketAddress.sin_addr.s_impno,

		client->conn->socketAddress.sin_addr.s_net,
		client->conn->socketAddress.sin_addr.s_host,
		client->conn->socketAddress.sin_addr.s_lh,
		client->conn->socketAddress.sin_addr.s_impno,

		StoreAgent.agent.officialName,
		AGENT_NAME,
		timeBuffer);
	fprintf(tmp, "Return-Path: <%s>\r\n", sender);
	
	// now try to append the mail content itself.
	while (!feof(msgfile)) {
		unsigned int count;
		
		count = fread(buffer, sizeof(char), sizeof(buffer), msgfile);
		if (count > 0) {
			if (count != fwrite(buffer, sizeof(char), count, tmp)) {
				goto ioerror;
			}
		} else if (ferror(msgfile)) {
			goto ioerror;
		}
	}
	
	// all done 
	newmail.size = (uint64_t) ftell(tmp);
	
	if (fsync(fileno(tmp)) != 0) {
		goto ioerror;
	}
	
	fclose(tmp);
	tmp = NULL;
	
	// try to create store object.
	newmail.type = STORE_DOCTYPE_MAIL;
	
	if (StoreObjectCreate(client, &newmail)) {
		printf("Couldn't create new mail object\n");
		goto ioerror;
	}
	newmail.collection_guid = collection.guid;
	newmail.flags = flags;
	newmail.time_created = newmail.time_modified = (uint64_t) time(NULL);
	
	StoreProcessDocument(client, &newmail, tmppath);
	
	// need to acquire lock from this point
	
	// now we have guid etc., move the mail into the right place
	if (MaildirDeliverTempDocument(client, collection.guid, tmppath, newmail.guid)) {
		printf("Can't put temporary mail into the store\n");
		StoreObjectRemove(client, &newmail);
		goto ioerror;
	}
	
	// all done!
	StoreObjectFixUpFilename(&collection, &newmail);
	StoreObjectSave(client, &newmail);
	StoreObjectUpdateImapUID(client, &newmail); // FIXME: racy?
	
	// release lock here?
	
	// announce new mail has arrived
	++client->stats.insertions;
	StoreWatcherEvent(client, &newmail, STORE_WATCH_EVENT_NEW);

	
	return DELIVER_SUCCESS;

ioerror:
	if (tmp) {
		fclose(tmp);
		unlink(tmppath);
	}
	return DELIVER_TRY_LATER;
}

/* login */
CCode
SelectUser(StoreClient *client, char *user, char *password, int nouser)
{
    CCode ccode = -1;
    char buf[INET_ADDRSTRLEN+1];

    if (StoreAgent.installMode) {
        // Don't let users login in install mode.
        // FIXME: a better error message? 
        ccode = ConnWriteStr(client->conn, MSG4224NOUSER);
        goto finish;
    }

    if (0 != MsgAuthFindUser(user)) {
        if (IS_MANAGER(client)) {
            ccode = ConnWriteStr(client->conn, MSG4224NOUSER);
        } else {
            ccode = ConnWriteStr(client->conn, MSG3242BADAUTH);
            XplDelay(2000);
        }
        goto finish;
    }

    if (password && MsgAuthVerifyPassword(user, password) != 0) {
        ccode = ConnWriteStr(client->conn, MSG3242BADAUTH);
        XplDelay(2000);
        goto finish;
    }

    // FIXME: I think we're supposed to refer to the correct store IP?
    strncpy(buf, "127.0.0.1", INET_ADDRSTRLEN);
    ccode = ConnWriteF(client->conn, "1000 %s\r\n", buf);

    if (nouser) {
        goto finish;
    }

    UnselectUser(client);

    client->principal.type = STORE_PRINCIPAL_USER;
    strncpy(client->principal.name, user, sizeof(client->principal.name));
    client->principal.name[sizeof(client->principal.name) - 1] = 0;
    client->flags |= STORE_CLIENT_FLAG_IDENTITY;

finish:
    return ccode;
}

/* opens the store for the given user */
/* returns: -1 on error */

int 
SelectStore(StoreClient *client, char *user)
{
	const char *storeRoot = NULL;
	char path[XPL_MAX_PATH + 1];
	struct stat sb;
	
	// check if we already have this store selected
	if (client->storeName && !strcmp(user, client->storeName)) {
		return 0;
	}
	
	// close current selected store if necessary
	UnselectStore(client);
	
	snprintf(path, XPL_MAX_PATH, "%s/%s/", StoreAgent.store.rootDir, user);
	strcpy(client->store, path);
	
	client->storeRoot = storeRoot;
	client->storeHash = BongoStringHash(user);
	client->storeName = MemStrdup(user);
	
	client->stats.insertions = 0;
	client->stats.updates = 0;
	client->stats.deletions = 0;
	
	if (stat(path, &sb)) {
		// this store hasn't been created yet
		if (XplMakeDir(path)) {
			Log(LOG_ERROR, "Error creating store directory: %s.", strerror(errno));
			return -1;
		}
		if (StoreDBOpen(client, user)) {
			Log(LOG_ERROR, "Couldn't open store database for %s", user);
			return -2;
		}
		if (StoreObjectDBCreate(client)) {
			Log(LOG_ERROR, "Couldn't setup initial store database for %s", user);
			return -3;
		}
	} else {
		if (StoreDBOpen(client, user)) {
			Log(LOG_ERROR, "Couldn't access store database for %s", user);
			return -4;
		}
	}
	
	return 0;
}


/* closes the current store, if necessary */
void
UnselectStore(StoreClient *client)
{
	if (client->storeName && 
	client->stats.insertions + client->stats.updates + client->stats.deletions >
	(rand() % 50))
	{
		// FIXME: did optimise the Index here - could do a vacuum? could be costly tho...
	}

	if (client->watch.collection) {
		NLockStruct *lock;
		
		StoreGetCollectionLock(client, &lock, client->watch.collection);
		
		if (StoreWatcherRemove(client, client->watch.collection))
			Log(LOG_ERROR, "Internal error removing client watch");
		
		StoreReleaseCollectionLock(client, &lock);
	}

	StoreDBClose(client);
	client->storedb = NULL;

	if (client->storeName) {
		MemFree(client->storeName);
		client->storeName = NULL;
	}
}

void
SetStoreReadonly(StoreClient *client, char const* reason)
{
	client->readonly = TRUE;
	client->ro_reason = reason;
}

void
UnsetStoreReadonly(StoreClient *client)
{
	client->readonly = FALSE;
	client->ro_reason = NULL;
}

void
UnselectUser(StoreClient *client)
{
    StoreToken *tok;
    StoreToken *next;

    for (tok = client->principal.tokens; 
         tok; 
         tok = next) 
    {
        next = tok->next;
        MemFree(tok);
    }
    client->principal.tokens = NULL;
    client->principal.type = STORE_PRINCIPAL_NONE;
    client->flags &= ~STORE_CLIENT_FLAG_IDENTITY;
}


static int
RemoveCurrentDirectory()
{
    DIR *dir;
    struct dirent *dirent;
    struct stat sb;
    int result = -1;

    dir = opendir(".");
    if (!dir) {
        return -1;
    }

    while ((dirent = readdir(dir))) {
        if (!strcmp(".", dirent->d_name) ||
            !strcmp("..", dirent->d_name))
        {
            continue;
        }

        if (stat(dirent->d_name, &sb)) {
            goto finish;
        }
        
        if (S_ISREG(sb.st_mode)) {
            unlink(dirent->d_name);
        } else if (S_ISDIR(sb.st_mode)) {
            if (chdir(dirent->d_name) ||
                RemoveCurrentDirectory() ||
                chdir(".."))
            {
                goto finish;
            }                             
        }
    }
    result = 0;

finish:
    closedir(dir);

    return result;
}


void
DeleteStore(StoreClient *client)
{
    char path[XPL_MAX_PATH];
    int cwd;

    strncpy(path, client->store, sizeof(path));
    cwd = open(".", O_RDONLY);
    if (cwd < 0) {
        return;
    }
    
    client->flags |= STORE_CLIENT_FLAG_DONT_CACHE;

    UnselectStore(client);
    
    if (chdir(path)) {
        return;
    }
    RemoveCurrentDirectory();
    fchdir(cwd);
}

BongoJsonResult
GetJson(StoreClient *client, StoreObject *object, BongoJsonNode **node, char *filepath)
{
    BongoJsonResult ret = BONGO_JSON_UNKNOWN_ERROR;
    char path[XPL_MAX_PATH + 1];
    FILE *fh = NULL;
    char *buf = NULL;
    
    if (filepath) {
        strncpy(path, filepath, XPL_MAX_PATH);
    } else {
        FindPathToDocument(client, object->collection_guid, object->guid, path, sizeof(path));
    }

    fh = fopen(path, "rb");
    if (!fh) {
        Log(LOG_ERROR, "Couldn't open file for doc " GUID_FMT, object->guid);
        goto finish;
    }

    /* FIXME: would be nice (and easy) to get a streaming API for
     * bongojson */ 
    buf = MemMalloc(object->size + 1);
    
    if (fread(buf, 1, object->size, fh) == object->size) {
        buf[object->size] = '\0';        
    } else {
        Log(LOG_ERROR, "Couldn't read doc " GUID_FMT, object->guid);
        goto finish;
    }
    
    ret = BongoJsonParseString(buf, node);

    if (ret != BONGO_JSON_OK) {
        Log(LOG_ERROR, "Couldn't parse json object %s", buf);
    }
    
finish :
    fclose (fh);
    if (buf) MemFree(buf);

    return ret;    
}


/** lock convenience functions **/

/* gets an exclusive lock on the collection. 
 */

CCode 
StoreGetCollectionLock(StoreClient *client, NLockStruct **lock, uint64_t coll)
{
	int result;
	
	if (client == NULL) return 0;
	
	*lock = MemMalloc0(sizeof(NLockStruct));
	result = StoreGetExclusiveFairLockQuiet(client, &((*lock)->fairlock), coll, 3000);
	if (result != 0) {
		return ConnWriteStr(client->conn, MSG4120BOXLOCKED);
	} 
	return 0;
}

CCode
StoreReleaseCollectionLock(StoreClient *client, NLockStruct **lock)
{
	if (client == NULL) return 0;
	
	StoreReleaseFairLockQuiet(&((*lock)->fairlock));
	MemFree(*lock);
	*lock = NULL;
	return 0;
}

/** Pooled collection locks **/

typedef struct {
    uint64_t guid;
    NLockStruct *lock;
} colldatum;


int 
CollectionLockPoolInit(StoreClient *client, CollectionLockPool *pool)
{
    pool->client = client;
    return BongoArrayInit(&pool->colls, sizeof(colldatum), 8);
}


void
CollectionLockPoolDestroy(CollectionLockPool *pool)
{
    unsigned int i;

    for (i = 0; i < pool->colls.len; i++) {
        colldatum coll = BongoArrayIndex(&pool->colls, colldatum, i);

        StoreReleaseCollectionLock(pool->client, &coll.lock);
    }

    BongoArrayDestroy(&pool->colls, TRUE);
}


NLockStruct *
CollectionLockPoolGet(CollectionLockPool *pool, uint64_t guid)
{
    colldatum coll;
    unsigned int i;
    
    for (i = 0; i < pool->colls.len; i++) {
        colldatum coll = BongoArrayIndex(&pool->colls, colldatum, i);
        
        if (guid == coll.guid) {
            return coll.lock;
        }
    }

    if (StoreGetCollectionLock(pool->client, &coll.lock, guid)) {
        return NULL;
    }
    coll.guid = guid;
    BongoArrayAppendValue(&pool->colls, coll);
    return coll.lock;
}


