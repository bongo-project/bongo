/* This program is free software, licensed under the terms of the GNU GPL.
 * See the Bongo COPYING file for full details
 * Copyright (c) 2007 Alex Hudson
 */

#ifndef BONGOCONFIG_H
#define BONGOCONFIG_H

/* cli opts */
typedef struct {
	BOOL verbose;
	BOOL interactive;
	char *ip;
	char *dns;
} BongoConfig;

typedef struct {
    Connection *conn;
    char buffer[CONN_BUFSIZE + 1];
} StoreClient;

#define malloc(bytes) MemMalloc(bytes)
#define free(ptr) MemFree(ptr)

/* function prototypes */

void	usage(void);
void	RunAsBongoUser(void);
BOOL	NMAPSimpleCommand(StoreClient *client, char *command);
BOOL	SetAdminRights(StoreClient *client, char *document);
BOOL	PutOrReplaceConfig(StoreClient *client, char *collection, char *filename, char *content);
void	InitialStoreConfiguration(void);
void    GetInteractiveData(char *description, char **data, char *def);
BOOL	GenerateCryptoData(void);
void	CheckVersion(void);
void	TzCache(void);

/* bongo manager config is an object. One key should be 'agents', whose value
 * is an array of agent objects. Agent objects can have names, pri (priority)
 * and be enabled. Priorities are roughly in terms of dependency and/or which
 * order we want things to start in. Low pri == important, high pri == do this
 * last.
 */

static char *bongo_manager_config =  
"{ \"version\": 1, \n"
"  \"agents\": [ \n"
"   	{ \"name\": \"bongoqueue\", \"pri\": 3, \"enabled\": true },\n"
"	{ \"name\": \"bongosmtp\", \"pri\": 3, \"enabled\": true },\n"
"	{ \"name\": \"bongoantispam\", \"pri\": 5, \"enabled\": true },\n"
"	{ \"name\": \"bongoavirus\", \"pri\": 5, \"enabled\": true },\n"
"	{ \"name\": \"bongocollector\", \"pri\": 7, \"enabled\": true },\n"
"	{ \"name\": \"bongomailprox\", \"pri\": 7, \"enabled\": true },\n"
"	{ \"name\": \"bongoconnmgr\", \"pri\": 2, \"enabled\": true },\n"
"	{ \"name\": \"bongopluspack\", \"pri\": 5, \"enabled\": true },\n"
"	{ \"name\": \"bongorules\", \"pri\": 2, \"enabled\": true },\n"
"	{ \"name\": \"bongoimap\", \"pri\": 10, \"enabled\": true },\n"
"	{ \"name\": \"bongopop3\", \"pri\": 10, \"enabled\": true },\n"
"	{ \"name\": \"bongocalcmd\", \"pri\": 7, \"enabled\": true }\n"
"  ]\n"
"}\n";

static char *bongo_aspam_config =
"{ \"version\": 1, \n"
"  \"enabled\": true\n"
"}\n";

static char *bongo_avirus_config =
"{ \"version\": 1, \n"
"  \"enabled\": true, \n"
"  \"flags\": 168\n"
"}\n";

/* compat with older gnutls */
#ifndef GNUTLS_DIG_SHA1
	// older GNUTLS
#	define GNUTLS_SELF_SIGN(c, k) 	gnutls_x509_crt_sign((c), (c), (k))
#elif
	// new GNUTLS
#	define GNUTLS_SELF_SIGN(c, k)	gnutls_x509_crt_sign2((c), (c), (k), GNUTLS_DIG_SHA1, 0)
#endif

#endif
