/* This program is free software, licensed under the terms of the GNU GPL.
 * See the Bongo COPYING file for full details
 * Copyright (c) 2007 Alex Hudson
 */

#include "config.h"
#include <xpl.h>
#include <nmlib.h>
#include <bongostore.h>
#include <bongoagent.h>
#include <gcrypt.h>

#include "config.h"

/* cli opts */
struct {
    int verbose;

} BongoConfig;

typedef struct {
    Connection *conn;
    char buffer[CONN_BUFSIZE + 1];
} StoreClient;

#define malloc(bytes) MemMalloc(bytes)
#define free(ptr) MemFree(ptr)

void
usage() {
	static char *usage = 
		"bongo-config - Configure parts of the Bongo system.\n\n"
		"Usage: bongo-config [options] [command]\n\n"
		"Options:\n"
		"  -v, --verbose                   verbose output\n\n"
		"Commands:\n"
		"  install                         do the initial Bongo install\n"
		"";

	XplConsolePrintf("%s", usage);
}

BOOL
NMAPSimpleCommand(StoreClient *client, char *command) {
	CCode ccode;
	
	NMAPSendCommandF(client->conn, command);
	ccode = NMAPReadAnswer(client->conn, client->buffer, sizeof(client->buffer), TRUE);
	if (1000 == ccode)
		return TRUE;

	return FALSE;
}

BOOL
PutOrReplaceConfig(StoreClient *client, char *collection, char *filename, char *content) {
	int len;
	CCode ccode;
	char command[1024];

	len = strlen(content);

	snprintf(command, 1000, "INFO %s/%s\r\n", collection, filename);
	ccode = NMAPSendCommandF(client->conn, command);
	ccode = NMAPReadAnswer(client->conn, client->buffer, sizeof(client->buffer), TRUE);
	while (ccode == 2001)
		ccode = NMAPReadAnswer(client->conn, client->buffer, sizeof(client->buffer), TRUE);

	if (ccode == 1000) {
		// INFO returned OK, document already exists
		snprintf(command, 1000, "REPLACE %s/%s %d\r\n", 
			collection, filename, len);
	} else {
		// INFO errored, so document doesn't exist
		snprintf(command, 1000, "WRITE %s %d %d \"F%s\"\r\n", 
			collection, STORE_DOCTYPE_CONFIG, len, filename);
	}

	NMAPSendCommandF(client->conn, command);
	ccode = NMAPReadAnswer(client->conn, client->buffer, sizeof(client->buffer), TRUE);

	if (ccode == 2002) {
		ConnWrite(client->conn, content, len);
		ConnFlush(client->conn);
		if (NMAPReadAnswer(client->conn, client->buffer, 
		  sizeof(client->buffer), TRUE) == 1000) {
			return TRUE;
		}
		XplConsolePrintf("ERROR: Couldn't write data\n");
	} else {
		XplConsolePrintf("ERROR: Wouldn't accept data\n");
	}
	return FALSE;
}

void
InitialStoreConfiguration() {
	char path[XPL_MAX_PATH];
	int store_pid;
	char *args[3];
	
	store_pid = fork();
	if (store_pid == 0) { 	/* child */
		static char *install = "--install";

		snprintf(path, XPL_MAX_PATH, "%s/%s", XPL_DEFAULT_BIN_DIR,
			"bongostore");
		args[0] = path;
		args[1] = install;
		args[2] = NULL;
		
		execv(path, args);
		XplConsolePrintf("ERROR: Couldn't start the store!\n");
		exit(1);
	} else {		/* parent */
		StoreClient *client;
		CCode ccode = 0;
		int result = 0;

		XplDelay(1000); // FIXME: small hack, wait for store to start.
		
		client = malloc(sizeof(StoreClient));
		if (!client) {
			XplConsolePrintf("ERROR: Out of memory\n");
			goto storecleanup;
		}
		client->conn = NMAPConnect("127.0.0.1", NULL);
		if (!client->conn) {
			XplConsolePrintf("ERROR: Could not connect to store\n");
			goto storecleanup;
		}
		if (!NMAPAuthenticate(client->conn, client->buffer, sizeof(client->buffer))) {
			XplConsolePrintf("ERROR: Could not authenticate to store\n");
			goto nmapcleanup;
		}
		
		if (!NMAPSimpleCommand(client, "STORE _system\r\n")) {
			XplConsolePrintf("ERROR: Couldn't access store\n");
			goto nmapcleanup;
		}
		result = NMAPSimpleCommand(client, "CREATE /config\r\n");
		if ((result==1000) || (result==4226)) {
			// collection can't be created and doesn't exist
			XplConsolePrintf("ERROR: Couldn't create collection\n");
			goto nmapcleanup;
		}
		
		if (!PutOrReplaceConfig(client, "/config", "manager", bongo_manager_config)) {
			XplConsolePrintf("ERROR: couldn't write /config/manager\n");
		}
		if (!PutOrReplaceConfig(client, "/config", "avirus", bongo_avirus_config)) {
			XplConsolePrintf("ERROR: couldn't write /config/avirus\n");
		}

nmapcleanup:
		NMAPQuit(client->conn);
		ConnFree(client->conn);
storecleanup:		
		if (kill(store_pid, SIGTERM) != 0) {
			XplConsolePrintf("ERROR: Couldn't shut down store\n");
		}
	}
}

BOOL
GenerateCryptoData() {
	// save a random seed for faster Xpl startup in future.
	XplSaveRandomSeed();
}

int 
main(int argc, char *argv[]) {
	int next_arg = 0;
	int command = 0;
	int startup = 0;
	BongoAgent configtool;
	BongoConfig.verbose = 0;

	if (!MemoryManagerOpen("bongo-config")) {
		XplConsolePrintf("ERROR: Failed to initialize memory manager\n");
		return 1;
	}

	XplInit();

	startup = BA_STARTUP_CONNIO;	
	if (-1 == BongoAgentInit(&configtool, "bongoconfig", "", DEFAULT_CONNECTION_TIMEOUT, startup)) {
		XplConsolePrintf("ERROR : Couldn't initialize Bongo libraries\n");
		return 2;
	}

	/* parse options */
	while (++next_arg < argc && argv[next_arg][0] == '-') {
		if (!strcmp(argv[next_arg], "-v") || !strcmp(argv[next_arg], "--verbose")) {
			BongoConfig.verbose = 1;
		} else {
			printf("Unrecognized option: %s\n", argv[next_arg]);
		}
	}

	/* parse command */
	if (next_arg < argc) {
		if (!strcmp(argv[next_arg], "install")) { 
			command = 1;
		} else {
			printf("Unrecognized command: %s\n", argv[next_arg]);
		}
	} else {
		printf("ERROR: No command specified\n");
		usage();
	}

	switch(command) {
		case 1:
			InitialStoreConfiguration();
			break;
		default:
			XplConsolePrintf("ERROR: An internal error");
	}

	MemoryManagerClose("bongo-config");
	exit(0);
}
