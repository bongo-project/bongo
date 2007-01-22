#include <string.h>
#include "mdbldapp.h"

#define CHARS_WHITESPACE " \t\n\f\r"
#define CHARS_DIGIT "0123456789"
#define CHARS_ALPHA_UPPER "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
#define CHARS_ALPHA_LOWER "abcdefghijklmnopqrstuvwxyz"
#define CHARS_ALPHA CHARS_ALPHA_UPPER CHARS_ALPHA_LOWER
#define CHARS_ALPHANUM CHARS_DIGIT CHARS_ALPHA
#define CHARS_WORD "_" CHARS_ALPHANUM
#define CHARS_WHSP CHARS_WHITESPACE
#define CHARS_OID "-." CHARS_WORD

/**
 * Find the first character not among the characters in 'except'.
 */
static char *strnot(const char *string, const char *except) {
    char *s = (char *) string;
    char *e = (char *) except;

    while (*s != '\0') {
        while (*e != '\0') {
            if (*s == *e) {
                s++;
                break;
            }
            e++;
        }
        if (*e == '\0') {
            break;
        }
        e = (char *) except;
    }
    return s;
}

/**
 * Return true if c is among the characters in 'accept'.
 */
static int charin(int c, const char *accept) {
    char *a = (char *) accept;

    while (*a != '\0') {
        if (c == *a) {
            return 1;
        }
        a++;
    }
    return 0;
}

static char *ParseOid(char *ptr, char **oid) {
    if (!charin(*ptr, CHARS_OID)) {
        return NULL;
    }
    *oid = ptr;
    if (!(ptr = strchr(ptr, ' '))) {
        return NULL;
    }
    *ptr++ = '\0';
    return ptr;
}

static char *ParseQdescr(char *ptr, char **qdescr) {
    if (*ptr != '\'') {
        return NULL;
    }
    *qdescr = ++ptr;
    if (!(ptr = strchr(ptr, '\''))) {
        return NULL;
    }
    *ptr++ = '\0';
    return ptr;
}

static char *ParseNoidlen(char *ptr, char **noidlen) {
    /* Active Directory sometimes (incorrectly) uses quotes here */
    if (*ptr == '\'') {
        return ParseQdescr(ptr, noidlen);
    }

    if (!charin(*ptr, CHARS_OID)) {
        return NULL;
    }
    *noidlen = ptr;
    if (!(ptr = strchr(ptr, ' '))) {
    }
    *ptr++ = '\0';
    return ptr;
}

static char *ParseOids(char *ptr, char ***oids) {
    int inc = 4;
    int idx = 0;

    *oids = NewValArray(inc);
    if (!*oids) {
        return NULL;
    }

    if (*ptr == '(') {
        ptr = strnot(ptr + 1, CHARS_WHSP);
        while (ptr && *ptr != ')') {
            if (!(idx % inc)) {
                if (!ExpandValArray(oids, idx + inc)) {
                    FreeValArray(*oids);
                    free(*oids);
                    return NULL;
                }
            }
            if (!(ptr = ParseOid(ptr, &((*oids)[idx++]))) ||
                !(ptr = strnot(ptr, "$" CHARS_WHSP))) {
                break;
            }
        }
        if (!ptr || *ptr != ')') {
            FreeValArray(*oids);
            free(*oids);
            return NULL;
        }
    } else if (charin(*ptr, CHARS_OID)) {
        ptr = ParseOid(ptr, &((*oids)[idx++]));
    } else {
        FreeValArray(*oids);
        free(*oids);
        return NULL;
    }
    return ptr;
}

static char *ParseQdescrs(char *ptr, char ***qdescrs) {
    int inc = 4;
    int idx = 0;

    *qdescrs = NewValArray(inc);
    if (!*qdescrs) {
        return NULL;
    }

    if (*ptr == '(') {
        ptr = strnot(ptr + 1, CHARS_WHSP);
        while (ptr && *ptr != ')') {
            if (!(idx % inc)) {
                if (!ExpandValArray(qdescrs, idx + inc)) {
                    FreeValArray(*qdescrs);
                    free(*qdescrs);
                    return NULL;
                }
            }
            if (!(ptr = ParseQdescr(ptr, &((*qdescrs)[idx++]))) ||
                !(ptr = strnot(ptr, CHARS_WHSP))) {
                break;
            }
        }
        if (!ptr || *ptr != ')') {
            FreeValArray(*qdescrs);
            free(*qdescrs);
            return NULL;
        }
    } else if (*ptr == '\'') {
        if (!(ptr = ParseQdescr(ptr, &((*qdescrs)[idx++])))) {
            FreeValArray(*qdescrs);
            free(*qdescrs);
            return NULL;
        }
    } else {
        FreeValArray(*qdescrs);
        free(*qdescrs);
        return NULL;
    }
    return ptr;
}

BOOL ParseAttributeTypeDescription(char *ptr, MDBLDAPSchemaAttr *attr) {
    char *tmp;

    ptr = strnot(ptr, CHARS_WHSP);
    if (*ptr != '(') {
        return FALSE;
    }

    if (!(ptr = strnot(ptr + 1, CHARS_WHSP)) ||
        !(ptr = ParseOid(ptr, &attr->oid))) {
        return FALSE;
    }

    if ((tmp = strstr(ptr, "NAME"))) {
        ptr = tmp;
        if (!(ptr = strnot(ptr + 4, CHARS_WHSP)) ||
            !(ptr = ParseQdescrs(ptr, &attr->names))) {
            return FALSE;
        }
    }

    if ((tmp = strstr(ptr, "SUP"))) {
        ptr = tmp;
        if (!(ptr = strnot(ptr + 3, CHARS_WHSP)) ||
            !(ptr = ParseOid(ptr, &attr->sup))) {
            return FALSE;
        }
    }

    if ((tmp = strstr(ptr, "EQUALITY"))) {
        ptr = tmp;
        if (!(ptr = strnot(ptr + 8, CHARS_WHSP)) ||
            !(ptr = ParseOid(ptr, &attr->equality))) {
            return FALSE;
        }
    }

    if ((tmp = strstr(ptr, "SYNTAX"))) {
        ptr = tmp;
        if (!(ptr = strnot(ptr + 6, CHARS_WHSP)) ||
            !(ptr = ParseNoidlen(ptr, &attr->syntax))) {
            return FALSE;
        }
    }

    if (!(ptr = strnot(ptr, CHARS_WHSP)) &&
        !strncmp(ptr, "SINGLE-VALUE", 12)) {
        attr->singleval = TRUE;
    }

    return TRUE;
}

BOOL ParseObjectClassDescription(char *ptr, MDBLDAPSchemaClass *class) {
    char *tmp;

    ptr = strnot(ptr, CHARS_WHSP);
    if (*ptr != '(') {
        return FALSE;
    }

    if (!(ptr = strnot(ptr + 1, CHARS_WHSP)) ||
        !(ptr = ParseOid(ptr, &class->oid))) {
        return FALSE;
    }

    if ((tmp = strstr(ptr, "NAME"))) {
        ptr = tmp;
        if (!(ptr = strnot(ptr + 4, CHARS_WHSP)) ||
            !(ptr = ParseQdescrs(ptr, &class->names))) {
            return FALSE;
        }
    }

    if ((tmp = strstr(ptr, "SUP"))) {
        ptr = tmp;
        if (!(ptr = strnot(ptr + 3, CHARS_WHSP)) ||
            !(ptr = ParseOids(ptr, &class->sup))) {
            return FALSE;
        }
    }

    /*
    ptr = strnot(ptr, CHARS_WHSP);
    if (!strncmp(ptr, "ABSTRACT", 8) || 
        !strncmp(ptr, "STRUCTURAL", 10) || 
        !strncmp(ptr, "AUXILIARY", 9)) {
        type = ptr;
        if (!(ptr = strchr(ptr, ' '))) {
            return FALSE;
        }
        *ptr++ = '\0';
    }
    */

    if ((tmp = strstr(ptr, "MUST"))) {
        ptr = tmp;
        if (!(ptr = strnot(ptr + 4, CHARS_WHSP)) ||
            !(ptr = ParseOids(ptr, &class->must))) {
            return FALSE;
        }
    }

    if ((tmp = strstr(ptr, "MAY"))) {
        ptr = tmp;
        if (!(ptr = strnot(ptr + 3, CHARS_WHSP)) || 
            !(ptr = ParseOids(ptr, &class->may))) {
            return FALSE;
        }
    }

    return TRUE;
}
