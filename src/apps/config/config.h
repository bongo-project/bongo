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
    BongoJsonNode *domains;
} BongoConfig;

typedef struct {
    Connection *conn;
    char buffer[CONN_BUFSIZE + 1];
} StoreClient;

#define malloc(bytes) MemMalloc(bytes)
#define free(ptr) MemFree(ptr)

/* function prototypes */

// config.c
void    GetInstallParameters(void);
void    AddDomain(const char *str);
void    SetStoreConfigurationModifications(StoreClient *client);
void	LoadDefaultStoreConfiguration(void);
void	InitializeDataArea(void);
void	usage(void);
void	RunAsBongoUser(void);
void	RunAsRoot(void);
void	InitialStoreConfiguration(void);
void    GetInteractiveData(char *description, char **data, char *def);
BOOL	GenerateCryptoData(void);
void	CheckVersion(void);
void	AddUser(const char *username);
void	TzCache(void);

void	UserAdd(const char *username);
void	UserList(void);
void	UserPassword(const char *username);

// store.c
BOOL	ImportSystemBackupFile(StoreClient *client, const char *path);
BOOL	NMAPSimpleCommand(StoreClient *client, char *command);
BOOL	SetAdminRights(StoreClient *client, char *document);
BOOL	PutOrReplaceConfig(StoreClient *client, char *collection, char *filename, char *content, long len);

/* compat with older gnutls */
#ifndef GNUTLS_DIG_SHA1
	// older GNUTLS
#	define GNUTLS_SELF_SIGN(c, k) 	gnutls_x509_crt_sign((c), (c), (k))
#else
	// new GNUTLS
#	define GNUTLS_SELF_SIGN(c, k)	gnutls_x509_crt_sign2((c), (c), (k), GNUTLS_DIG_SHA1, 0)
#endif

#endif
