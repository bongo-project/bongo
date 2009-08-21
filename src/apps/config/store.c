// Load / save backup sets from the Store

#include <xpl.h>
#include <stdio.h>
#include <nmlib.h>
#include <msgapi.h>
#include <bongostore.h>
#include "config.h"

#include <libintl.h>
#define _(x) gettext(x)

BOOL
ImportSystemBackupFile(StoreClient *client, const char *path)
{
	FILE *config;
	char header[513];
	CCode result;
	
	config = fopen(path, "r");
	if (config == NULL) {
		XplConsolePrintf(_("ERROR: Couldn't load default config set\n"));
		return FALSE;
	}
	while(512 == fread(&header, sizeof(char), 512, config)) {
		long filesize, blocksize, read;
		size_t len;
		char str_filesize[13], filename[101];
		char *file;
		header[512] = 0;
		
		memcpy(&filename, &header, 100);
		filename[100] = 0;
		memcpy(&str_filesize, &header[124], 12);
		str_filesize[12] = 0;
		filesize = strtol(str_filesize, NULL, 8);
		len = strlen(filename);
		
		if (! strncmp(filename, "", 100)) {
			// end of tar is _two_ empty blocks... ah well :o>
			break;
		}
		
		blocksize = filesize / 512;
		if (filesize % 512) blocksize++;
		blocksize *= 512;

		/* if this is a directory we need to create a new collection */
		if (filename[len-1] == '/') {
			char currentpath[110];
			char fullpath[110];

			filename[len-1] = 0;

			snprintf(currentpath, 100, "/%s", filename);
			snprintf(fullpath, 108, "CREATE %s\r\n", currentpath);
			result = NMAPSimpleCommand(client, fullpath);

			if ((result==1000) || (result==4226)) {
				// collection can't be created and doesn't exist
				XplConsolePrintf(_("ERROR: Couldn't create collection: %s\n"), currentpath);
				return FALSE;
			}
			if (! SetAdminRights(client, currentpath)) {
				XplConsolePrintf(_("ERROR: Couldn't set acls on %s\n"), currentpath);
				return FALSE;
			}
			continue;
		}

        /* now, do everything else that isn't a directory and contains data */
		if (blocksize > 0 && filename[len-1] != '/') {
			char fullpath[110];
			char *delim;

			file = MemMalloc((size_t) blocksize);
			read = fread(file, sizeof(char), blocksize, config);
			if (read != blocksize) {
				XplConsolePrintf("  Read error!");
			}
			
			delim = strrchr(filename, '/');
			*delim = 0;
			snprintf(fullpath, 108, "/%s", filename);
			if (!PutOrReplaceConfig(client, fullpath,
				delim+1, file, filesize)) {
				XplConsolePrintf(_("ERROR: Couldn't write %s to store\n"), fullpath);
			} else {
				if (! SetAdminRights(client, fullpath)) {
					XplConsolePrintf(_("ERROR: Couldn't set rights on store file %s\n"), fullpath);
				}
			}
			MemFree(file);
		}
	}
	
	fclose(config);
	return TRUE;
}

BOOL
SetAdminRights(StoreClient *client, char *document) {
	CCode ccode;

	ccode = NMAPRunCommandF(client->conn, client->buffer, sizeof(client->buffer),
		"ACL GRANT %s Pall Tuser Wadmin\r\n", document);
	if (ccode == 1000)
		return TRUE;
	return FALSE;
}

BOOL
PutOrReplaceConfig(StoreClient *client, char *collection, char *filename, char *content, long len) {
	CCode ccode;
	char command[1024];

	snprintf(command, 1000, "INFO %s/%s\r\n", collection, filename);
	ccode = NMAPSendCommandF(client->conn, "%s", command);
	ccode = NMAPReadAnswer(client->conn, client->buffer, sizeof(client->buffer), TRUE);
	while (ccode == 2001)
		ccode = NMAPReadAnswer(client->conn, client->buffer, sizeof(client->buffer), TRUE);

	if (ccode == 1000) {
		// INFO returned OK, document already exists
		snprintf(command, 1000, "REPLACE %s/%s %ld\r\n", 
			collection, filename, len);
	} else {
		// INFO errored, so document doesn't exist
		snprintf(command, 1000, "WRITE %s %d %ld \"F%s\"\r\n", 
			collection, STORE_DOCTYPE_CONFIG, len, filename);
	}

	NMAPSendCommandF(client->conn, "%s", command);
	ccode = NMAPReadAnswer(client->conn, client->buffer, sizeof(client->buffer), TRUE);

	if (ccode == 2002) {
		ConnWrite(client->conn, content, len);
		ConnFlush(client->conn);
		if (NMAPReadAnswer(client->conn, client->buffer, 
		  sizeof(client->buffer), TRUE) == 1000) {
			return TRUE;
		}
		XplConsolePrintf(_("ERROR: Couldn't write data\n"));
	} else {
		XplConsolePrintf(_("ERROR: Wouldn't accept data\n"));
	}
	return FALSE;
}

BOOL
NMAPSimpleCommand(StoreClient *client, char *command) {
	CCode ccode;
	
	NMAPSendCommandF(client->conn, "%s", command);
	ccode = NMAPReadAnswer(client->conn, client->buffer, sizeof(client->buffer), TRUE);
	if (1000 == ccode)
		return TRUE;

	return FALSE;
}
