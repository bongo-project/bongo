// Bongo copyright applies
// (C) Alex Hudson 2007

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

void
FindPathToCollection(StoreClient *client, uint64_t collection, char *dest, size_t size)
{
	snprintf(dest, size, "%s" GUID_FMT, client->store, collection);
	dest[size-1] = '\0';
}

void
FindPathToDocument(StoreClient *client, uint64_t collection, uint64_t document,
char *dest, size_t size)
{
	snprintf(dest, size, "%s" GUID_FMT "/cur/" GUID_FMT, client->store, collection, document);
	dest[size-1] = '\0';
}

/** Create a new maildir. If it already exists, we don't error
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

void
MaildirTempDocument(StoreClient *client, uint64_t collection, char *dest, size_t size)
{
	snprintf(dest, size, "%s" GUID_FMT "/tmp/XXXXXXXX", client->store, collection);
	dest[size-1] = '\0';
	mkstemp(dest);
}

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
