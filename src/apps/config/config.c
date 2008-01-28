/* This program is free software, licensed under the terms of the GNU GPL.
 * See the Bongo COPYING file for full details
 * Copyright (c) 2007 Alex Hudson
 */

#include <xpl.h>
#include <stdio.h>
#include <nmlib.h>
#include <msgapi.h>
#include <bongo-buildinfo.h>
#include <bongostore.h>

#include <bongoagent.h>

#include <gnutls/gnutls.h>
#include <gnutls/x509.h>
#include <gcrypt.h>
#include <time.h>
#include <sys/stat.h>
#include <sys/types.h>
#include "config.h"

#include <libintl.h>
#define _(x) gettext(x)

BongoConfig config;

void
usage(void) {
	char *text = 
		"bongo-config - Configure parts of the Bongo system.\n\n"
		"Usage: bongo-config [options] [command]\n\n"
		"Options:\n"
		"  -v, --verbose                   verbose output\n"
		"  -s, --silent                    non-interactive mode\n\n"
		"Commands:\n"
		"  user add <user>                 add a user to the systen\n"
		"  user list                       list the users on the system\n"
		"  user password <user>            set the password of a user\n"
		"  install                         do the initial Bongo install\n"
		"  crypto                          regenerate data needed for encryption\n"
		"  checkversion                    see if a newer Bongo is available\n" 
		"  tzcache                         regenerate cached timezone information\n"
		"";

	XplConsolePrintf("%s", text);
}

void
RunAsBongoUser() 
{
	if (XplSetEffectiveUser(MsgGetUnprivilegedUser()) < 0) {    
		printf(_("bongo-config: Could not drop to unprivileged user '%s'\n"), MsgGetUnprivilegedUser());
		exit(-1);
	}
}

void
RunAsRoot()
{
	if (XplSetEffectiveUserId(0) < 0) {
		XplConsolePrintf(_("bongo-config: Couldn't get back root privs.\n"));
	}
}

void
AddDomain(const char *str) {
        BongoJsonNode *domain;

        domain = BongoJsonNodeNewString(str);
        BongoArrayAppendValue(BongoJsonNodeAsArray(config.domains), domain);
}

void
GetInstallParameters(void){
    char *tmp = NULL;

    GetInteractiveData(_("IP address to run on"), &config.ip, "127.0.0.1");
    GetInteractiveData(_("DNS name to use as main hostname:"), &config.dns, "localhost");

    /* this is a little messy here due to the way the array works */
    if (!config.domains) {
        config.domains = BongoJsonNodeNewArray(BongoArrayNew(sizeof(char *), 1));
        while (1) {
            GetInteractiveData(_("Mail Domains (enter \"\" to end adding domains):"), &tmp, "");
            if (tmp[0] == '\0') {
                tmp = NULL;
                break;
            }

            AddDomain(tmp);
            tmp = NULL;
        }
    }
}

void
SetStoreConfigurationModifications(StoreClient *client) {
    BongoJsonNode *node;
    unsigned char *aconfig = NULL;
    unsigned char *default_config = NULL;
    unsigned int default_config_len;
    /*
     * unsigned char default_config[] = "{ \"domainalias\" : \"\", \"aliases\" : { \"postmaster\" : \"admin\" }, \"username-mapping\" : 0 }";
    const unsigned int default_config_len = strlen(default_config);

    */

    NMAPReadConfigFile("aliases/default_config", &default_config);
    if (default_config == NULL) return;
    default_config_len = strlen(default_config);

    /* first let's read in the queue document and store out the domains and hosted domains */
    if(NMAPReadConfigFile("queue", &aconfig)) {
        if (aconfig == NULL) return;
        if (BongoJsonParseString(aconfig, &node) == BONGO_JSON_OK) {
            BongoJsonNode *current;
            char *content;
            unsigned int x;
            BongoArray *domains;

            current = BongoJsonJPath(node, "o:domains/a");
            domains = BongoJsonNodeAsArray(config.domains);
            /* free the old array */
            BongoArrayDestroy(BongoJsonNodeAsArray(current), TRUE);
            BongoJsonNodeAsArray(current) = domains;

            content = BongoJsonNodeToString(node);
            PutOrReplaceConfig(client, "/config", "queue", content, strlen(content));

            /* add the default configuration document for each domain */
            for(x=0;x<domains->len;x++) {
                BongoJsonNode *current = BongoJsonArrayGet(domains, x);
                PutOrReplaceConfig(client, "/config/aliases", BongoJsonNodeAsString(current), default_config, default_config_len);
            }
            BongoJsonNodeFree(node);
        }
        MemFree(aconfig);
        aconfig = NULL;
    }

    /* now store out the global file changes */
    if (NMAPReadConfigFile("global", &aconfig)) {
        if (aconfig == NULL) return;
        if (BongoJsonParseString(aconfig, &node) == BONGO_JSON_OK) {
            char *content;
            BongoJsonNode *current;

            current = BongoJsonJPath(node, "o:hostname/s");
            MemFree(BongoJsonNodeAsString(current));
            BongoJsonNodeAsString(current) = MemStrdup(config.dns);

            current = BongoJsonJPath(node, "o:hostaddr/s");
            MemFree(BongoJsonNodeAsString(current));
            BongoJsonNodeAsString(current) = MemStrdup(config.ip);

            current = BongoJsonJPath(node, "o:postmaster/s");
            MemFree(BongoJsonNodeAsString(current));
            BongoJsonNodeAsString(current) = MemStrdup("admin");

            content = BongoJsonNodeToString(node);
            PutOrReplaceConfig(client, "/config", "global", content, strlen(content));

            BongoJsonNodeFree(node);
        }
        MemFree(aconfig);
        aconfig = NULL;
    }
}

void
InitializeDataArea(void)
{
	MsgApiDirectory dir;
	const unsigned char *unpriv_user;
	uid_t uid = 0;
	gid_t gid = 0;

	unpriv_user = MsgGetUnprivilegedUser();
	if (unpriv_user != NULL)
		XplLookupUser(unpriv_user, &uid, &gid);
	
	XplConsolePrintf(_("Creating directory structure...\n"));
	for (dir = MSGAPI_DIR_START; dir < MSGAPI_DIR_END; dir++) {
		char path[XPL_MAX_PATH];
		if (MsgGetDir(dir, path, XPL_MAX_PATH)) {
			MsgMakePath(path);
			chown(path, uid, gid);
		}
	}
	
	RunAsBongoUser();
	
	XplConsolePrintf(_("Initializing user database...\n"));
	if (MsgAuthInit() != 0) {
		XplConsolePrintf(_("ERROR: Couldn't initialise auth subsystem\n"));
		exit(1);
	}
	if (MsgAuthInstall() != 0) {
		XplConsolePrintf(_("Warning: Couldn't create user database\n"));
	}
	
	if (!MsgSetServerCredential()) {
		XplConsolePrintf(_("ERROR: Cannot create server credential\n"));
		exit(2);
	}
}

void
InitialStoreConfiguration(void) {
	BOOL cal_success;
	
	// attempt to initialise cal lib. This creates cached data, and can be time
	// consuming: do it now, rather than in the store (for now, at least)
	XplConsolePrintf(_("Initializing calendar data...\n"));
	cal_success = BongoCalInit(XPL_DEFAULT_DBF_DIR);
	if (! cal_success) {
		XplConsolePrintf(_("ERROR: Couldn't initialise calendar library\n"));
		exit(1);
	}
	
	XplConsolePrintf(_("Loading initial store configuration...\n"));
	LoadDefaultStoreConfiguration();
}

void
LoadDefaultStoreConfiguration(void)
{
	char path[XPL_MAX_PATH];
	int store_pid;
	char *args[3];
	
	store_pid = fork();
	if (store_pid == 0) { 	// child 
		static char *install = "--install";

		snprintf(path, XPL_MAX_PATH, "%s/%s", XPL_DEFAULT_BIN_DIR,
			"bongostore");
		args[0] = path;
		args[1] = install;
		args[2] = NULL;
		
		execv(path, args);
		XplConsolePrintf(_("ERROR: Couldn't start the store!\n"));
		exit(1);
	} else {		// parent 
		StoreClient *client;

		XplDelay(1000); // FIXME: small hack, wait for store to start.
		
		client = malloc(sizeof(StoreClient));
		if (!client) {
			XplConsolePrintf(_("ERROR: Out of memory\n"));
			goto storecleanup;
		}
		client->conn = NMAPConnect("127.0.0.1", NULL);
		if (!client->conn) {
			XplConsolePrintf(_("ERROR: Could not connect to store\n"));
			goto storecleanup;
		}
		if (!NMAPAuthenticateToStore(client->conn, client->buffer, sizeof(client->buffer))) {
			XplConsolePrintf(_("ERROR: Could not authenticate to store\n"));
			goto nmapcleanup;
		}
		
		if (!NMAPSimpleCommand(client, "STORE _system\r\n")) {
			XplConsolePrintf(_("ERROR: Couldn't access store\n"));
			goto nmapcleanup;
		}

		XplConsolePrintf(_("Setting default agent configuration...\n"));

		snprintf(path, XPL_MAX_PATH, "%s/conf/default.set", XPL_DEFAULT_DATA_DIR);
		if (! ImportSystemBackupFile(client, path)) {
			XplConsolePrintf(_("ERROR: Couldn't setup default configuration\n"));
		} else {
			XplConsolePrintf(_("Complete.\n"));
		}

        SetStoreConfigurationModifications(client);
nmapcleanup:
		NMAPQuit(client->conn);
		ConnFree(client->conn);
storecleanup:		
		if (kill(store_pid, SIGTERM) != 0) {
			XplConsolePrintf(_("ERROR: Couldn't shut down store\n"));
		}
	}
}

void
GetInteractiveData(char *description, char **data, char *def) {
	int size = 128;
	char *line;

	if (*data != NULL)
		// don't ask for information we already have
		return;

	XplConsolePrintf("%s [%s]: ", description, def);
	*data = (char *)MemMalloc(sizeof(char)*size);
	line = fgets(*data, size, stdin);
	if (!line || *line == '\0' || *line == '\n' || *line == '\r') {
		MemFree(*data);
		*data = MemStrdup(def);
	}
	
	size = strlen(line);
	if (line[size-1] == '\n') line[size-1] = 0;
}

#define CERTSIZE	10240

BOOL
GenerateCryptoData() {
	gnutls_dh_params_t dh_params;
	gnutls_rsa_params_t rsa_params;
	gnutls_x509_privkey key;
	gnutls_x509_crt certificate;
	unsigned char dhdata[CERTSIZE], rsadata[CERTSIZE], private_key[CERTSIZE], public_cert[CERTSIZE];
	int ret;
	size_t dsize = sizeof(unsigned char) * (CERTSIZE);
	int cert_version = 1;
	mode_t private = S_IRUSR | S_IWUSR;
	FILE *params;
	static const char *org = "Bongo Project";
	static const char *unit = "Fake Certs Dept.";
	static const char *state = "None";
	static const char *country = "US";
	static const char *blank = "";
	
	gnutls_global_init();

	// hack: we need to ensure the files below can be saved...
	if (0 != mkdir(XPL_DEFAULT_DBF_DIR, 0755)) {
		XplConsolePrintf(_("Couldn't create data directory!\n"));
	}
	
	// save a random seed for faster Xpl startup in future.
	XplConsolePrintf(_("Creating random seed...\n"));
	XplSaveRandomSeed();

	// various magic params
	XplConsolePrintf(_("Creating DH parameters...\n"));
	gnutls_dh_params_init(&dh_params);
	gnutls_dh_params_generate2(dh_params, 1024);
	dsize = CERTSIZE;
	gnutls_dh_params_export_pkcs3 (dh_params, GNUTLS_X509_FMT_PEM, dhdata, &dsize);
	params = fopen(XPL_DEFAULT_DHPARAMS_PATH, "w");
	chmod(XPL_DEFAULT_DHPARAMS_PATH, private);
	if (params) {
		fprintf(params, "%s\n", dhdata);
		fclose(params);
	} else {
		XplConsolePrintf(_("ERROR: Couldn't write DH params to %s\n"), XPL_DEFAULT_DHPARAMS_PATH);
		return FALSE;
	}

	XplConsolePrintf(_("Creating RSA parameters...\n"));	
	gnutls_rsa_params_init(&rsa_params);
	gnutls_rsa_params_generate2(rsa_params, 512);
	dsize = CERTSIZE;
	gnutls_rsa_params_export_pkcs1 (rsa_params, GNUTLS_X509_FMT_PEM, rsadata, &dsize);
	params = fopen(XPL_DEFAULT_RSAPARAMS_PATH, "w");	
	if (params) {
		chmod(XPL_DEFAULT_RSAPARAMS_PATH, private);
		fprintf(params, "%s\n", dhdata);
		fclose(params);
	} else {
		XplConsolePrintf(_("ERROR: Couldn't write RSA params to %s\n"), XPL_DEFAULT_DHPARAMS_PATH);
		return FALSE;
	}

	// create private key
	XplConsolePrintf(_("Creating private key...\n"));
	ret = gnutls_x509_privkey_init(&key);
	if (ret < 0) {
		XplConsolePrintf(_("ERROR: %s\n"), gnutls_strerror (ret));
		return FALSE;
	}
	ret = gnutls_x509_privkey_generate(key, GNUTLS_PK_RSA, 1024, 0);
	if (ret < 0) {
		XplConsolePrintf(_("ERROR: Couldn't create private key. %s\n"), gnutls_strerror(ret));
		return FALSE;
	}
	dsize = CERTSIZE;
	ret = gnutls_x509_privkey_export(key, GNUTLS_X509_FMT_PEM, &private_key, &dsize);
	if (ret < 0) {
		XplConsolePrintf(_("ERROR: Couldn't export private key. %s\n"), gnutls_strerror(ret));
		XplConsolePrintf(_("       Size: %d, Ret: %d\n"), dsize, ret);
		return FALSE;
	} else {
		params = fopen(XPL_DEFAULT_KEY_PATH, "w");
		if (params) {
			chmod(XPL_DEFAULT_KEY_PATH, private);
			fprintf(params, "%s\n", private_key);
			fclose(params);
		} else {
			XplConsolePrintf(_("ERROR: Couldn't write private key to %s\n"), XPL_DEFAULT_KEY_PATH);
			return FALSE;
		}
	}

	// create certificate
	XplConsolePrintf(_("Creating SSL/TLS certificate...\n"));
	ret = gnutls_x509_crt_init(&certificate);
	if (ret < 0) {
		XplConsolePrintf(_("ERROR: %s\n"), gnutls_strerror(ret));
		return FALSE;
	}
	gnutls_x509_crt_set_activation_time(certificate, time(NULL));
	gnutls_x509_crt_set_expiration_time(certificate, time(NULL) + (700 * 24 * 60 * 60));
	gnutls_x509_crt_set_key(certificate, key);
	gnutls_x509_crt_set_version(certificate, 1);
	gnutls_x509_crt_set_serial(certificate, &cert_version, sizeof(int));

	gnutls_x509_crt_set_dn_by_oid(certificate, 
		GNUTLS_OID_X520_ORGANIZATION_NAME, 0, org, strlen(org));
	gnutls_x509_crt_set_dn_by_oid(certificate, 
		GNUTLS_OID_X520_ORGANIZATIONAL_UNIT_NAME, 0, unit, strlen(unit));
	gnutls_x509_crt_set_dn_by_oid(certificate, 
		GNUTLS_OID_X520_STATE_OR_PROVINCE_NAME, 0, state, strlen(state));
	gnutls_x509_crt_set_dn_by_oid(certificate, 
		GNUTLS_OID_X520_COUNTRY_NAME, 0, country, strlen(country));
	gnutls_x509_crt_set_dn_by_oid(certificate,
		GNUTLS_OID_X520_LOCALITY_NAME, 0, blank, strlen(blank));
	gnutls_x509_crt_set_dn_by_oid(certificate, 
		GNUTLS_OID_X520_COMMON_NAME, 0, config.dns, strlen(config.dns));
	gnutls_x509_crt_set_dn_by_oid(certificate,
		GNUTLS_OID_LDAP_UID, 0, blank, strlen(blank));
	gnutls_x509_crt_set_subject_alternative_name (certificate,
		GNUTLS_SAN_DNSNAME, config.dns);
	
	gnutls_x509_crt_set_key_usage(certificate, GNUTLS_KEY_DIGITAL_SIGNATURE |
		GNUTLS_KEY_DATA_ENCIPHERMENT);

	ret = GNUTLS_SELF_SIGN(certificate, key);
	if (ret < 0) {
		XplConsolePrintf(_("ERROR: Couldn't self-sign certificate. %s\n"), gnutls_strerror(ret));
		return FALSE;
	}

	dsize = CERTSIZE;
	ret = gnutls_x509_crt_export(certificate, GNUTLS_X509_FMT_PEM, &public_cert, &dsize);
	if (ret < 0) { 
		XplConsolePrintf(_("ERROR: Couldn't create certificate. %s\n"), gnutls_strerror(ret));
		return FALSE;
	}
	params = fopen(XPL_DEFAULT_CERT_PATH, "w");
	if (params) {
		fprintf(params, "%s\n", public_cert);
		fclose(params);
	} else {
		XplConsolePrintf(_("ERROR: Couldn't write certificate to %s\n"), XPL_DEFAULT_CERT_PATH);
		return FALSE;
	}
	
	gnutls_x509_crt_deinit(certificate);	
	gnutls_x509_privkey_deinit(key);
	
	return TRUE;
}

void
CheckVersion() {
	int current_version, available_version;
	BOOL custom_version;

	if (!MsgGetBuildVersion(&current_version, &custom_version)) {
		XplConsolePrintf(_("Can't get information on current build!\n"));
		return;
	} else {
		XplConsolePrintf(_("Installed build: %s %s%d\n"), BONGO_BUILD_BRANCH, BONGO_BUILD_VSTR, current_version);
		if (custom_version)
			XplConsolePrintf(_("This is modified software.\n"));
	}
	if (!MsgGetAvailableVersion(&available_version)) {
		XplConsolePrintf(_("Version currently available: unable to request online information.\n"));
	} else {
		XplConsolePrintf(_("Version currently available: %s%d\n"), BONGO_BUILD_VSTR, available_version);
	}
}

void
UserAdd(const char *username) 
{
	int result = MsgAuthAddUser(username);
	switch (result) {
		case 0:
			XplConsolePrintf(_("Added user %s\n"), username);
			break;
		case -1:
			XplConsolePrintf(_("User %s already exists\n"), username);
			break;
		case -2:
			XplConsolePrintf(_("Auth backend doesn't support adding user\n"));
			break;
		default:
			XplConsolePrintf(_("Couldn't add user %s: internal error\n"), username);
			break;
	}	
}

void
UserList(void)
{
	// TODO
}

void
UserPassword(const char *username) 
{
	char *password = NULL;
	char *password2 = NULL;
	
	GetInteractiveData(_("New password for the user"), &password, "");
	GetInteractiveData(_("Confirm the password"), &password2, "");
	
	if (strlen(password) == 0) {
		XplConsolePrintf(_("ERROR: Can't set password to empty string.\n"));
		return;
	}
	if (strncmp(password, password2, strlen(password))) {
		XplConsolePrintf(_("ERROR: The passwords provided don't match.\n"));
		return;
	}
	
	if (MsgAuthSetPassword(username, password) != 0) {
		XplConsolePrintf(_("ERROR: Couldn't set the password for the user.\n"));
	}
}

void
TzCache() {
	char path[PATH_MAX];
	char *dbfdir = XPL_DEFAULT_DBF_DIR;

	snprintf(path, PATH_MAX, "%s/%s", dbfdir, "bongo-tz.cache");
	unlink(path);
    
	BongoCalInit(dbfdir);
}

int 
main(int argc, char *argv[]) {
	int next_arg = 0;
	int command = 0;
	int startup = 0;
	BongoAgent configtool;
	config.verbose = 0;

    /* this just clears up a warning.  we don't need this here */
    GlobalConfig[0].type = BONGO_JSON_NULL;

	if (!MemoryManagerOpen("bongo-config")) {
		XplConsolePrintf("ERROR: Failed to initialize memory manager\n");
		return 1;
	}
	
	// parse options
	while (++next_arg < argc && argv[next_arg][0] == '-') {
		char *arg = argv[next_arg];
		if (!strcmp(arg, "-v") || !strcmp(arg, "--verbose")) {
			config.verbose = TRUE;
		} else if (!strcmp(arg, "-s") || !strcmp(arg, "--silent")) {
			config.interactive = FALSE;
		} else if (!strcmp(arg, "--ip")) {
			next_arg++;
			config.ip = MemStrdup(argv[next_arg]);
		} else if (!strcmp(arg, "--hostname")) {
			next_arg++;
			config.dns = MemStrdup(argv[next_arg]);
        } else if (!strcmp(arg, "--domain")) {
            if (!config.domains) {
               config.domains = BongoJsonNodeNewArray(BongoArrayNew(sizeof(char *), 1)); 
            }
            next_arg++;
            AddDomain(argv[next_arg]);
		} else {
			printf("Unrecognized option: %s\n", argv[next_arg]);
		}
	}

	// parse command 
	if (next_arg < argc) {
		if (!strcmp(argv[next_arg], "install")) { 
			command = 1;
		} else if (!strcmp(argv[next_arg], "crypto")) {
			command = 2;
		} else if (!strcmp(argv[next_arg], "checkversion")) {
			command = 3;
		} else if (!strcmp(argv[next_arg], "tzcache")) {
			command = 4;
		} else if (!strcmp(argv[next_arg], "user")) {
			command = 5;
		} else {
			printf("Unrecognized command: %s\n", argv[next_arg]);
		}
	} else {
		printf("ERROR: No command specified\n");
		usage();
		return 1;
	}

	// unusual libgcrypt options must occur before XplInit()
	if ((command == 2) || (command == 1)) {
		gcry_control (GCRYCTL_ENABLE_QUICK_RANDOM, 0);
	}
	XplInit();

	// we don't want to setup libraries unless we need to (e.g., to run an agent)
	if (command == 1) {
		startup = BA_STARTUP_CONNIO;
		if (-1 == BongoAgentInit(&configtool, "bongoconfig", "", DEFAULT_CONNECTION_TIMEOUT, startup)) {
			XplConsolePrintf(_("ERROR : Couldn't initialize Bongo libraries\n"));
			return 2;
		}
	}

	next_arg++;
	switch(command) {
		case 1:
            GetInstallParameters();
			InitializeDataArea(); // changes our user to unprivileged
			GenerateCryptoData();
			RunAsRoot();
			InitialStoreConfiguration();
			break;
		case 2:
			RunAsBongoUser();
			GenerateCryptoData();
			break;
		case 3:
			RunAsBongoUser();
			CheckVersion();
			break;
		case 4:
			RunAsBongoUser();
			TzCache();
			break;
		case 5:
			if (MsgAuthInit()) {
				XplConsolePrintf(_("Couldn't initialise auth subsystem\n"));
				return 3;
			}
			if ((next_arg + 1) >= argc) {
				XplConsolePrintf(_("USAGE : user [add|password] <username>\n"));
			} else {
				char *command = argv[next_arg++];
				char *username = argv[next_arg];
				
				if (! strncmp(command, "add", 3)) {
					UserAdd(username);
				} else if (!strncmp(command, "password", 8)) {
					UserPassword(username);
				} else if (!strncmp(command, "list", 4)) {
					UserList();
				} else {
					XplConsolePrintf(_("ERROR: Unknown command '%s' on user\n"), command);
				}
			}
			break;
		default:
			break;
	}

    if (config.ip) {
        MemFree(config.ip);
    }

    if (config.dns) {
        MemFree(config.dns);
    }

    if (config.domains) {
        BongoJsonNodeFree(config.domains);
    }

	MemoryManagerClose("bongo-config");
	exit(0);
}
