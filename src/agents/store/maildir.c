// Bongo copyright applies
// (C) Alex Hudson 2007

/** \file
 * Various utility functions for dealing with maildir storage
 */

#include <config.h>
#include <xpl.h>

// mkdir ()
#include <sys/stat.h>
#include <sys/types.h>
#include <errno.h>

// readdir ()
#include <sys/types.h>
#include <dirent.h>

// mkstemp ()
#include <stdlib.h>

#include "stored.h"

/**
 * Find the path to a directory representing the maildir for a collection on disk
 * \param	client		Calling store client
 * \param	collection	UID of the collection we're looking for
 * \param	dest		The path is written to this buffer
 * \param	size		Size of the path buffer allocating
 * \return 	Nothing
 */
void
FindPathToCollection(StoreClient *client, uint64_t collection, char *dest, size_t size)
{
	snprintf(dest, size, "%s" GUID_FMT, client->store, collection);
	dest[size-1] = '\0';
}

/**
 * Find the path to a file representing the file in a maildir for a document on disk
 * \param	client		Calling store client
 * \param	collection	UID of the collection we're looking for
 * \param	document	UID of the document we want
 * \param	dest		The path is written to this buffer
 * \param	size		Size of the path buffer allocating
 * \return 	Nothing
 */
void
FindPathToDocument(StoreClient *client, uint64_t collection, uint64_t document,
char *dest, size_t size)
{
	snprintf(dest, size, "%s" GUID_FMT "/cur/" GUID_FMT, client->store, collection, document);
	dest[size-1] = '\0';
}

/** 
 * Create a new maildir. If it already exists, we don't error
 * \param	store_path	Path to the file store to contain the collection we want to create
 * \param	collection	UID of the collection we want to represent
 * \return	0 on success, <0 otherwise
 */
int
MaildirNew(const char *store_path, uint64_t collection)
{
	const char *dirs[] = { "/", "/cur", "/new", "/tmp", NULL };
	char rootpath[XPL_MAX_PATH];
	char subdir[XPL_MAX_PATH + 5];
	int i;
	
	snprintf(rootpath, XPL_MAX_PATH, "%s" GUID_FMT, store_path, collection);
	
	for (i = 0; dirs[i] != NULL; i++) {
		int res = 0;
		strncpy(subdir, rootpath, XPL_MAX_PATH);
		strncat(subdir, dirs[i], 5);
		res = mkdir(subdir, S_IRUSR | S_IWUSR | S_IXUSR | S_IRGRP | S_IWGRP);
		if ((res != 0) && (errno != EEXIST)) {
			return -4;
		}
		chmod(subdir, S_IRUSR | S_IWUSR | S_IXUSR);
	}
	return 0;
}

/**
 * Remove an existing maildir
 * \param 	store_path	Path to the file store containing the maildir we want to remove
 * \param	collection	UID of the collection we want to remove
 * \return	0 on success, some error otherwise
 */
int
MaildirRemove(const char *store_path, uint64_t collection)
{
	const char *dirs[] = { "/cur", "/new", "/tmp", NULL };
	char rootpath[XPL_MAX_PATH+1];
	int i;
	
	for (i = 0; dirs[i] != NULL; i++) {
		DIR *dir;
		struct dirent *content;
		
		snprintf(rootpath, XPL_MAX_PATH, "%s" GUID_FMT "%s", store_path, collection, dirs[i]);
		dir = opendir(rootpath);
		if (dir) {
			char filename[XPL_MAX_PATH+1];
			
			while ((content = readdir(dir)) != NULL) {
				char *c = content->d_name;
				if ((c[0] == '.') && ((c[1] == '.') || (c[1] == '\0'))){
					continue;
				}
				snprintf(filename, XPL_MAX_PATH, "%s/%s", rootpath, content->d_name);
				if (unlink(filename) == -1) {
					// can't remove one of the contents
					closedir(dir);
					return errno;
				}
			}
			closedir(dir);
		}
		if (rmdir(rootpath) != 0) return errno;
	}
	
	snprintf(rootpath, XPL_MAX_PATH, "%s" GUID_FMT, store_path, collection);
	if (rmdir(rootpath) != 0) return errno;
	
	return 0;
}

/**
 * Create a temporary file in a maildir
 * \param 	client		Store client
 * \param	collection	UID of the collection we want to put the document in
 * \param	dest		Buffer to contain the file path to the temp document
 * \param	size		Size of the buffer allocated in dest
 * \return	0 on success, -1 on error
 */
int
MaildirTempDocument(StoreClient *client, uint64_t collection, char *dest, size_t size)
{
	int fd;

	snprintf(dest, size, "%s" GUID_FMT "/tmp/XXXXXXXX", client->store, collection);
	dest[size-1] = '\0';
	fd = mkstemp(dest);
	if (fd != -1) {
		close(fd);
		unlink(dest);
		return 0;
	}
	return -1;
}

/**
 * Move a temporary file into it's final position based on allocation UID
 * \param 	client		Store client
 * \param	collection	UID of the collection we want to put the document in
 * \param	path		File path to the temporary document
 * \param	uid		Allocated uid of the document
 * \return	0 on success, -1 otherwise
 */
int
MaildirDeliverTempDocument(StoreClient *client, uint64_t collection, const char *path, uint64_t uid)
{
	char newpath[XPL_MAX_PATH+1];
	
	FindPathToDocument(client, collection, uid, &newpath, XPL_MAX_PATH);
	
	if (link(path, newpath) != 0) {
		// look at errno. EEXIST suggests we already delivered it; maybe ignore that?
		return -1;
	}
	unlink(path); // don't really care if this fails?
	return 0;
}
