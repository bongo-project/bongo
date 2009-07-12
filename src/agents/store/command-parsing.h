
#include "stored.h"

#define TOKEN_OK 0xffff /* magic */

CCode
CheckTokC(StoreClient *client, int n, int min, int max);

// parse simple data types

CCode	ParseGUID(StoreClient *client, char *token, uint64_t *out);
CCode	ParseUnsignedLong(StoreClient *client, char *token, unsigned long *out);
CCode	ParseInt(StoreClient *client, const char const *token, int *out);
CCode	ParseHexU32(StoreClient *client, char *token, uint32_t *out);
CCode	ParseStreamLength(StoreClient *client, char *token, int *out);
int 	ParseRange(StoreClient *client, char *token, int *startOut, int *endOut);
CCode	ParseISODateTime(StoreClient *client, char *token, BongoCalTime *timeOut);
CCode	ParseIcalDateTime(StoreClient *client, char *token, BongoCalTime *timeOut);
CCode	ParseDateTime(StoreClient *client, char *token, BongoCalTime *timeOut);
CCode	ParseDateTimeToUint64(StoreClient *client, char *token, uint64_t *timeOut);
CCode	ParseDateTimeToString(StoreClient *client, char *token, char *buffer, int buflen);
CCode	ParseDateRange(StoreClient *client, char *token, char *startOut, char *endOut);
CCode	ParseFlag(StoreClient *client, char *token, int *valueOut, int *actionOut);

// parse simple types and return complex data
CCode	ParseCollection(StoreClient *client, char *token, StoreObject *collection);
CCode	ParseDocumentInfo(StoreClient *client, char *token, StoreObject *info);

CCode	ParseDocument(StoreClient *client, char *token, StoreObject *object);
CCode	ParseCreateDocument(StoreClient *client, char *token, uint64_t *outGuid, StoreDocumentType type);
CCode 	ParseDocType(StoreClient *client, char *token, StoreDocumentType *out);
CCode	ParsePropName(StoreClient *client, char *token, StorePropInfo *prop);
CCode	ParsePropList(StoreClient *client, char *token, StorePropInfo * props, int propcount, int *count, int requireLengths);
CCode	ParsePrivilege(StoreClient *client, const char const *token, StorePrivilege *priv);
CCode	ParsePrincipal(StoreClient *client, const char const *token, StorePrincipalType *type);
CCode	ParseHeaderList(StoreClient *client, char *token, StoreHeaderInfo *headers, int size, int *count);
CCode	ParseStoreName(StoreClient *client, char *token);
CCode	ParseUserName(StoreClient *client, char *token);

// convert types into textual representations for the store protocol
int	PrivilegeToString(const StorePrivilege priv, char const **out);
int	PrincipalToString(const StorePrincipalType type, char const **out);
