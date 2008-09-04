
#ifndef _COMMAND_PARSING_H
#define _COMMAND_PARSING_H

#include "command-parsing.h"
#include "object-model.h"
#include "stored.h"
#include "messages.h"
#include "properties.h"

#define TOKEN_OK 0xffff /* magic */


typedef struct {
	const StorePrivilege priv;
	const char const *name;
} StorePrivName;

static const StorePrivName StorePrivilegeTable[] = {
	{ STORE_PRIV_WRITE, "write" },
	{ STORE_PRIV_WRITE_PROPS, "writeprops" },
	{ STORE_PRIV_WRITE_CONTENT, "writecontent" },
	{ STORE_PRIV_UNLOCK, "unlock" },
	{ STORE_PRIV_READ_ACL, "readacl" },
	{ STORE_PRIV_READ_OWN_ACL, "readownacl" },
	{ STORE_PRIV_WRITE_ACL, "writeacl" },
	{ STORE_PRIV_BIND, "bind" },
	{ STORE_PRIV_UNBIND, "unbind" },
	{ STORE_PRIV_READ_BUSY, "busy" },
	{ STORE_PRIV_READ_PROPS, "props" },
	{ STORE_PRIV_LIST, "list" },
	{ STORE_PRIV_READ, "read" },
	{ STORE_PRIV_ALL, "all" },
	{ 0, NULL }
};

CCode
ParsePrivilege(StoreClient *client, const char const *token, StorePrivilege *priv)
{
	StorePrivName const *priventry = StorePrivilegeTable;
	
	while (priventry->name != NULL) {
		if (strcmp(token, priventry->name) == 0) {
			*priv = priventry->priv;
			return TOKEN_OK;
		}
		priventry++;
	}
	return -1;
}

/**
 * Get the textual name of a store privilege. 
 * \param	priv	Privilege whose name we want.
 * \param	out	Pointer to the string name
 * \return	0 on success, error codes otherwise (and out will be NULL)
 */
// this is here because we have access to the parse table above.
int
PrivilegeToString(const StorePrivilege priv, char const **out)
{
	StorePrivName const *priventry = StorePrivilegeTable;
	
	*out = NULL;
	while (priventry->name != NULL) {
		if (priventry->priv == priv) {
			*out = priventry->name;
			return 0;
		}
		priventry++;
	}
	return -1;
}

CCode
ParsePrincipal(StoreClient *client, const char const *token, StorePrincipalType *type)
{
	if (strcmp(token, "user") == 0) {
		*type = STORE_PRINCIPAL_USER;
		return TOKEN_OK;
	} else if (strcmp(token, "group") == 0) {
		*type = STORE_PRINCIPAL_GROUP;
		return TOKEN_OK;
	} else if (strcmp(token, "all") == 0) {
		*type = STORE_PRINCIPAL_ALL;
		return TOKEN_OK;
	} else if (strcmp(token, "auth") == 0) {
		*type = STORE_PRINCIPAL_AUTHENTICATED;
		return TOKEN_OK;
	} else if (strcmp(token, "token") == 0) {
		*type = STORE_PRINCIPAL_TOKEN;
		return TOKEN_OK;
	} else {
		return -1;
	}
}

/**
 * Get a textual representation of the store principal.
 * \param	type	StorePrincipal we want the string for
 * \param	out	Place we're going to put the string
 * \return	0 if successful, error codes otherwise
 */
int
PrincipalToString(const StorePrincipalType type, char const **out)
{
	switch(type) {
		case STORE_PRINCIPAL_USER:
			*out = "user";
			return 0;
		case STORE_PRINCIPAL_GROUP:
			*out = "group";
			return 0;
		case STORE_PRINCIPAL_ALL:
			*out = "all";
			return 0;
		case STORE_PRINCIPAL_AUTHENTICATED:
			*out = "auth";
			return 0;
		case STORE_PRINCIPAL_TOKEN:
			*out = "token";
			return 0;
		default:
			*out = NULL;
			return -1;
	}
}

CCode
CheckTokC(StoreClient *client, int n, int min, int max)
{
	if (n < min || n > max) {
		return ConnWriteStr(client->conn, MSG3010BADARGC);
	}

	return TOKEN_OK;
}


CCode
ParseGUID(StoreClient *client, char *token, uint64_t *out)
{
	uint64_t t;
	char *endp;

	t = HexToUInt64(token, &endp);
	if (*endp || 0 == t /* || ULLONG_MAX == t */) {
		return ConnWriteStr(client->conn, MSG3011BADGUID);
	}

	*out = t;
	return TOKEN_OK;
}

CCode
ParseHexU32(StoreClient *client, char *token, uint32_t *out)
{
	uint64_t t;
	char *endp;
	
	t = HexToUInt64(token, &endp);
	if (*endp || (t > UINT32_MAX)) {
		return ConnWriteStr(client->conn, MSG3010BADARGC);
	}
	*out = (uint32_t) t;
	return TOKEN_OK;
}

CCode
ParseUnsignedLong(StoreClient *client, char *token, unsigned long *out)
{
	unsigned long t;
	char *endp;

	t = strtoul(token, &endp, 10);
	if (*endp) {
		return ConnWriteStr(client->conn, MSG3017BADINTARG);
	}
	*out = t;
	return TOKEN_OK;
}


CCode
ParseInt(StoreClient *client, const char const *token, int *out)
{
	long t;
	char *endp;

	t = strtol(token, &endp, 10);
	if (*endp) {
		return ConnWriteStr(client->conn, MSG3017BADINTARG);
	}
	*out = t;
	return TOKEN_OK;
}


CCode
ParseStreamLength(StoreClient *client, char *token, int *out) 
{
	long t;
	char *endp;

	if (token[0] == '-' && token[1] == '\0') {
		*out = -1;
		return TOKEN_OK;
	}

	t = strtol(token, &endp, 10);
	if (*endp) {
		return ConnWriteStr(client->conn, MSG3017BADINTARG);
	}

	*out = t;
	return TOKEN_OK;
}


int 
ParseRange(StoreClient *client, char *token, int *startOut, int *endOut) 
{
	long t;
	char *endp;

	if (!strcmp(token, "all")) {
		*startOut = *endOut = -1;
		return TOKEN_OK;
	}

	t = strtol(token, &endp, 10);
	if (endp == token || *endp != '-' || t < 0) {
		return ConnWriteStr(client->conn, MSG3020BADRANGEARG);
	}
	*startOut = t;

	t = strtol(endp + 1, &endp, 10);
	if (*endp /* || t < *startOut */) {
		return ConnWriteStr(client->conn, MSG3020BADRANGEARG);
	}
	*endOut = t;

	return TOKEN_OK;
}

CCode
ParseISODateTime(StoreClient *client,
                 char *token,
                 BongoCalTime *timeOut)
{
	/* iso-date-time: YYYY-MM-DD HH:MM:SS'Z' */
	int n;
	BongoCalTime t = BongoCalTimeEmpty();

	t = BongoCalTimeSetTimezone(t, BongoCalTimezoneGetUtc());

	n = sscanf(token, "%4d-%2d-%2d %2d:%2d:%2dZ",
			   &t.year, &t.month, &t.day,
			   &t.hour, &t.minute, &t.second);

	if (6 != n) {
		return ConnWriteStr(client->conn, MSG3021BADISODATETIME);
	}

	t.month--;

	*timeOut = t;

	return TOKEN_OK;
}

CCode
ParseIcalDateTime(StoreClient *client,
                  char *token,
                  BongoCalTime *timeOut)
{
	BongoCalTime t;

	t = BongoCalTimeParseIcal(token);
	if (BongoCalTimeIsEmpty(t)) {
		return ConnWriteStr(client->conn, MSG3021BADICALDATETIME);
	}

	*timeOut = BongoCalTimeSetTimezone(t, BongoCalTimezoneGetUtc());

	return TOKEN_OK;
}

CCode
ParseDateTime(StoreClient *client, char *token, BongoCalTime *timeOut)
{
	int len;

	len = strlen(token);

	if (len == 20) {
		return ParseISODateTime(client, token, timeOut);
	} else if (len == 15 || len == 16) {
		return ParseIcalDateTime(client, token, timeOut);
	} else {
		unsigned long ulong;
		CCode ccode;

		ccode = ParseUnsignedLong(client, token, &ulong);

		if (ccode == TOKEN_OK) {
			*timeOut = BongoCalTimeNewFromUint64(ulong, FALSE, NULL);
		}

		return ccode;
	}
}

CCode
ParseDateTimeToUint64(StoreClient *client, char *token, uint64_t *timeOut)
{
	CCode ccode;
	int len;

	len = strlen(token);

	if (len == 20 || len == 15 || len == 16) {
		BongoCalTime t;

		ccode = ParseDateTime(client, token, &t);
		if (ccode == TOKEN_OK) {
			*timeOut = BongoCalTimeAsUint64(t);
		}

		return ccode;
	} else {
		/* If it's a long arg, just pass it through */
		unsigned long ulong;
		CCode ccode;

		ccode = ParseUnsignedLong(client, token, &ulong);

		*timeOut = (uint64_t)ulong;

		return ccode;
	}
}

CCode
ParseDateTimeToString(StoreClient *client, char *token, char *buffer, int buflen)
{
	int len;

	len = strlen(token);

	if (len == 15 || len == 16) {
		/* FIXME: do a better validation */
		
		if (!isdigit(token[0]) || !isdigit(token[1]) || !isdigit(token[2]) || 
			!isdigit(token[3]) || !isdigit(token[4]) || !isdigit(token[5]) || 
			!isdigit(token[6]) || !isdigit(token[7]))
		{
			return ConnWriteStr(client->conn, MSG3021BADDATETIME);
		}
		
		if (len > 8) {
			if ('T' != token[8] || 
				!isdigit(token[9]) || !isdigit(token[10]) || !isdigit(token[11]) || 
				!isdigit(token[12]) || !isdigit(token[13]) || !isdigit(token[14]))
			{
				return ConnWriteStr(client->conn, MSG3021BADDATETIME);
			}
		}

		BongoStrNCpy(buffer, token, buflen);

		return TOKEN_OK;
	} else {
		BongoCalTime t;
		CCode ccode;
		
		ccode = ParseDateTime(client, token, &t);
		if (ccode == TOKEN_OK) {
			BongoCalTimeToIcal(t, buffer, buflen);
		}

		return ccode;
	}
}

CCode
ParseDateRange(StoreClient *client, char *token, char *startOut, char *endOut)
{
	/* date-range: date-time '-' date-time */

	CCode ccode;
	char *p;

	p = strchr(token, '-');
	if (!p) {
		return ConnWriteStr(client->conn, MSG3021BADDATERANGE);
	}
	*p = 0;
	ccode = ParseDateTimeToString(client, token, startOut, BONGO_CAL_TIME_BUFSIZE);
	if (TOKEN_OK != ccode) {
		return ccode;
	}
	++p;
	return ParseDateTimeToString(client, p, endOut, BONGO_CAL_TIME_BUFSIZE);
}


CCode
ParseFlag(StoreClient *client, char *token, int *valueOut, int *actionOut)
{
	long t;
	char *endp;

	if (*token == '+') {
		*actionOut = STORE_FLAG_ADD;
		token++;
	} else if (*token == '-') {
		*actionOut = STORE_FLAG_REMOVE;
		token++;
	} else {
		*actionOut = STORE_FLAG_REPLACE;
	}

	t = strtol(token, &endp, 10);
	if (*endp) {
		return ConnWriteStr(client->conn, MSG3017BADINTARG);
	}
	*valueOut = t;
	return TOKEN_OK;
}


CCode
ParseCollection(StoreClient *client, char *token, StoreObject *object) 
{
	int ccode;

	// try to find relevant storeobject; if it doesn't exist, say so.
	if ('/' != token[0]) {
		uint64_t guid;
		ccode = ParseGUID(client, token, &guid);
		if (TOKEN_OK != ccode) return ccode;
		
		ccode = StoreObjectFind(client, guid, object);
		if (ccode == -1) return ConnWriteStr(client->conn, MSG4220NOGUID);
	} else {
		ccode = StoreObjectFindByFilename(client, token, object);
		if (ccode == -1) return ConnWriteStr(client->conn, MSG4224NOCOLLECTION);
	}
	if (ccode != 0) return ConnWriteStr(client->conn, MSG5005DBLIBERR);

	// if this is a collection, we're ok    
	if (STORE_IS_FOLDER(object->type)) return TOKEN_OK;

	// this isn't a collection - have we enough perms to say it exists?
	if (StoreObjectCheckAuthorization(client, object, STORE_PRIV_READ) == 0)
		return ConnWriteStr(client->conn, MSG3015NOTCOLL);

	// otherwise, let's say it doesn't exist. 
	return ConnWriteStr(client->conn, MSG4224NOCOLLECTION);
}


CCode
ParseDocumentInfo(StoreClient *client, char *token, StoreObject *object)
{
	int ccode  = 0;
	uint64_t guid;
	
	if ('/' != token[0]) {
		ccode = ParseGUID(client, token, &guid);
		if (ccode != TOKEN_OK) return ccode;
		
		ccode = StoreObjectFind(client, guid, object);
		if (ccode == -1) return ConnWriteStr(client->conn, MSG4220NOGUID);
	} else {
	   // FIXME
	   //return (StoreCheckAuthorizationPath(client, token, STORE_PRIV_READ) ||
	   //             ConnWriteStr(client->conn, MSG4224NODOCUMENT));
	}
	
	if (ccode != 0) return ConnWriteStr(client->conn, MSG5005DBLIBERR);
	
	return TOKEN_OK;
}


CCode
ParseDocument(StoreClient *client, char *token, StoreObject *object)
{
	int ccode;

	if ('/' != token[0]) {
		// find object by guid
		uint64_t guid;
		
		ParseGUID(client, token, &guid);
		ccode = StoreObjectFind(client, guid, object);
		if (ccode == -1) return ConnWriteStr(client->conn, MSG4220NOGUID);
	} else {
		// get an object via filename
		ccode = StoreObjectFindByFilename(client, token, object);
		if (ccode == -1) return ConnWriteStr(client->conn, MSG4224NODOCUMENT);
	}

	if (ccode != 0) return ConnWriteStr(client->conn, MSG5005DBLIBERR);

	return TOKEN_OK;
}

CCode
ParseDocType(StoreClient *client, char *token, StoreDocumentType *out)
{
	long t;
	char *endp;

	t = strtol(token, &endp, 10);
	if (*endp) {
		return ConnWriteStr(client->conn, MSG3016DOCTYPESYNTAX);
	}

	*out = t;

	t &= ~(STORE_DOCTYPE_FOLDER | STORE_DOCTYPE_SHARED);
	if (t < STORE_DOCTYPE_UNKNOWN || t > STORE_DOCTYPE_CONFIG) {
		return ConnWriteStr(client->conn, MSG3015BADDOCTYPE);
	}

	return TOKEN_OK;
}

CCode
ParsePropName(StoreClient *client, char *token, StorePropInfo *prop)
{
	StorePropertyByName(token, prop);
	
	if (prop->type == STORE_PROP_BADPROP) {
		return ConnWriteStr(client->conn, MSG3244BADPROPNAME);
	}
	
	return TOKEN_OK;
}


CCode
ParsePropList(StoreClient *client,
              char *token,
              StorePropInfo * props,
              int propcount,
              int *count,
              int requireLengths)
{
	int i;
	char *p = token;
	char *q;
	CCode ccode = TOKEN_OK;

	for (i = 0; i < propcount && p && TOKEN_OK == ccode; i++) {
		(&props[i])->name = NULL;
		(&props[i])->type = 0;
		p = strchr(token, ','); 
		if (p && ',' == *p) {
			*p = 0;
		}
		q = strchr(token, '/');
		if (q) {
			ccode = ParseInt(client, q + 1, &props[i].valueLen);
			if (TOKEN_OK != ccode) {
				return ccode;
			}
			*q = 0;
		} else if (requireLengths) {
			return ConnWriteStr(client->conn, MSG3244BADPROPLENGTH);
		}
		ccode = ParsePropName(client, token, props + i);
		token = p + 1;
	}
	*count = i;
	return ccode;
}


CCode
ParseHeaderList(StoreClient *client, 
                char *token,
                StoreHeaderInfo *headers,
                int size,
                int *count)
{
	/* <headerlist> ::= (<headerlist> '&') <andclause>
	   <andclause>  ::= (<andclause> '|') <header> 
	   <header>     ::= ('!') <headername> '=' <value>
	*/

	int flags = HEADER_INFO_OR;
	int i;
	char *p = token;
	char *q;
	CCode ccode = TOKEN_OK;

	for (i = 0; i < size && p && TOKEN_OK == ccode; i++) {
		headers[i].flags = flags;

		p = strpbrk(token, "|&"); 
		if (p) {
			if ('&' == *p) {
				flags = HEADER_INFO_AND;
			} else {
				flags = HEADER_INFO_OR;
			}
			*p = 0;
		}
		if ('!' == *token) {
			headers[i].flags |= HEADER_INFO_NOT;
			++token;
		}
		q = strchr(token, '=');
		if (!q) {
			return ConnWriteStr(client->conn, MSG3022BADSYNTAX);
		}
		headers[i].value = q + 1;
		*q = 0;
		
		for (q = headers[i].value; *q; q++) {
			/* FIXME: not UTF-8 safe */
			*q = tolower(*q);
		}

		if (!strcmp("from", token)) {
			headers[i].type = HEADER_INFO_FROM;
		} else if (!strcmp("to", token)) {
			headers[i].type = HEADER_INFO_RECIP;
		} else if (!strcmp("list", token)) {
			headers[i].type = HEADER_INFO_LISTID;
		} else if (!strcmp("subject", token)) {
			headers[i].type = HEADER_INFO_SUBJECT;
		} else {
			return ConnWriteStr(client->conn, MSG3022BADSYNTAX);
		}

		token = p + 1;
	}
	*count = i;
	return ccode;
}

CCode
ParseStoreName(StoreClient *client,
               char *token) /* token will be modified */
{
	char *i;

	for (i = token; i < token + STORE_MAX_STORENAME; i++) {
		if (0 == *i) return TOKEN_OK;
	*i = tolower(*i);
	}

	return ConnWriteStr(client->conn, MSG3023USERNAMETOOLONG);
}

CCode
ParseUserName(StoreClient *client, char *token)
{
	return ParseStoreName(client, token);
}

#endif
