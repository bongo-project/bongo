/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

#define PRODUCT_NAME "MDB LDAP Driver"
#define PRODUCT_VERSION "$Revision: 1.1 $"
#define LOGGER "MDB LDAP Driver"

#include <logger.h>
#include "mdbldapp.h"
#include <bongoutil.h>
#include <memmgr.h>
#include <bongostream.h>

#define INIT_VS_LDAP(v) \
if (v->ldap == NULL) { \
    v->ldap = GetLdapConnection((MDBLDAPContextStruct *) v->context); \
}

#if 0
/* Linked list used to store handles.
   Similar methods are often used in MDB drivers to validate handles.
   Not sure if this is really necessary. */
typedef struct _HandleListNode {
    MDBHandle *Handle;
    _HandleListNode *Next;
} HandleListNode;
#endif

struct {
    char ServerTree[MDB_MAX_OBJECT_CHARS + 1];
    char ServerDn[MDB_MAX_OBJECT_CHARS + 1];
    char *MdbBaseDn;
    char *LdapBaseDn;
    char *LdapSchemaDn;
    char *LdapBindDn;
    char *LdapBindPasswd;
    char *LdapHostAddress;
    int LdapHostPort;
    int LdapMinConnections;
    int LdapMaxConnections;
    int LdapTls;
    LDAP *Ldap;
    XplMutex LdapMutex;
    BongoHashtable *PathTable;
    XplRWLock PathLock;
    BongoHashtable *AttrTable;
    BongoHashtable *ClassTable;
    BongoHashtable *TransTable;
    BongoHashtable *ExtTable;
    /*HandleListNode *HandleList; */
    char *args;
} MdbLdap;

typedef struct {
    char *str;
    int len;
    int size;
} StringBuilder;


static char *TranslateSchemaName(char *Name);

static void
AppendString(StringBuilder *sb, const char *toAppend)
{
    int fit;
    
    fit = strlen(toAppend);
    
    if (sb->size - sb->len <= fit) {
        int grow = 512;
        if (grow <= fit) {
            grow = fit + 1;
        }
        
        sb->size += grow;
        sb->str = realloc(sb->str, sb->size);
    }
    
    strcpy(sb->str + sb->len, toAppend);
    sb->len += fit;
}

static char *
vsprintf_dup (const char *format, va_list ap)
{
    char buf[1024];
    int n;
    
    va_list ap2;
    XPL_VA_COPY(ap2, ap);
    
    n = XplVsnprintf (buf, 1024, format, ap);
    
    if (n < 1024) {
        va_end (ap2);
        return strdup (buf);
    } else {
        char *ret;
        ret = malloc (n + 1);
        XplVsnprintf (ret, n + 1, format, ap2);
        va_end (ap2);
        return ret;
    }
}

static char *
sprintf_dup (const char *format, ...)
{
    va_list ap;
    char *ret;
    
    va_start(ap, format);
    ret = vsprintf_dup(format, ap);
    va_end(ap);
    
    return ret;    
}

#if 0
int
HandleListAdd(MDBHandle *Handle)
{
    HandleListNode *node = NULL;
    HandleListNode *curr = NULL;

    node = malloc(sizeof(HandleListNode));

    if (!node) {
        return -1;
    }

    node->Next = NULL;
    node->Handle = Handle;
    curr = MdbLdap.HandleList;

    if (curr) {
        while (curr->Next) {
            curr = curr->Next;
        }

        curr->Next = node;
    } else {
        MdbLdap.HandleList = node;
    }

    return 0;
}

MDBHandle *
HandleListFind(LDAP *Ldap)
{
    HandleListNode *curr = NULL;

    curr = MdbLdap.HandleList;

    while (curr) {
        if (curr->Handle->ldap == Ldap) {
            return curr->Handle;
        }

        curr = curr->Next;
    }

    return NULL;
}

int
HandleListRemove(MDBHandle *Handle) {
    HandleListNode *curr = NULL;
    HandleListNode *prev = NULL;

    curr = MdbLdap.HandleList;

    while (curr) {
        if (curr->Handle == Handle) {
            found = 1;
            break;
        }

        prev = curr;
        curr = curr->Next;
    }

    if (found) {
        if (prev) {
            prev->Next = curr->Next;
        } else {
            MdbLdap.HandleList = curr->Next;
        }

        free(curr);
        return 0;
    }

    return -1;
}
#endif

/* Function used for normal rebinding. */
static int
UserRebindProc(LDAP * ld, LDAP_CONST char *url, ber_tag_t request,
               ber_int_t msgid, void *params)
{
    /*MDBHandle handle = HandleListGet(ld); */
    MDBHandle handle = NULL;
    char *binddn = NULL;
    char *passwd = NULL;

    if (params) {
        handle = (MDBHandle) params;
        binddn = (char *) handle->binddn;
        passwd = (char *) handle->passwd;
    }

    return ldap_simple_bind_s(ld, binddn, passwd);
}

static int
DriverRebindProc(LDAP * ld, LDAP_CONST char *url, ber_tag_t request,
                 ber_int_t msgid, void *params)
{
    return ldap_simple_bind_s(ld, MdbLdap.LdapBindDn, MdbLdap.LdapBindPasswd);
}

/**
 * Create a new DN.
 * Remove the tree name and trailing slash.
 * Make sure delimiters aren't repeated.
 */
static char *
GetNewDn(const char *DnIn)
{
    char *newDn = NULL;
    char *tmpDn = NULL;
    char *token;
    char *ptr;

    tmpDn = strdup(DnIn);

    if (tmpDn) {
        token = strtok_r(tmpDn, "\\", &ptr);

        if (token) {
            if (MdbLdap.ServerTree[0] && !strcmp(token, MdbLdap.ServerTree)) {
                token = strtok_r(NULL, "\\", &ptr);
            }

            newDn = malloc(MDB_MAX_OBJECT_CHARS + 1);

            if (newDn) {
                newDn[0] = '\0';

                if (*DnIn == '\\') {
                    strcat(newDn, "\\");
                }
    
                while (token) {
                    strcat(newDn, token);
                    token = strtok_r(NULL, "\\", &ptr);

                    if (token) {
                        strcat(newDn, "\\");
                    }
                }
            }
        }

        free(tmpDn);
    }

    return newDn;
}

static char *
GetAbsoluteDn(const char *Object, const char *Context)
{
    char tmpDn[MDB_MAX_OBJECT_CHARS + 1];

    if (*Object == '\\') {
        /* already absolute */
        return GetNewDn(Object);
    } 

    if (!Context) {
        Context = MdbLdap.MdbBaseDn;
    }

    if (Object[0] == '.' && Object[1] == '\0') {
        return GetNewDn(Context);
    }
    
    /* relative */
    sprintf(tmpDn, "%s\\%s", Context, Object);
    return GetNewDn (tmpDn);
}

/**
 * Create a DN for Object that is relative to Context.
 * Returns an absolute DN if the Object is higher than Context.
 */
static char *
GetRelativeDn(const char *Object, const char *Context)
{
    char *mdbDn = NULL;
    char *tmpDn = NULL;

    if (*Object == '\\') {
        /* absolute */
        tmpDn = (char *)Object;

        if (Context) {
            if (strcmp(Object, Context) && !strncmp(Object, Context, 
                                                    strlen(Context))) {
                tmpDn += strlen(Context) + 1;
            }
        } else {
            if (strcmp(Object, MdbLdap.MdbBaseDn) && 
                !strncmp(Object, MdbLdap.MdbBaseDn, 
                         strlen(MdbLdap.MdbBaseDn))) {
                tmpDn += strlen(MdbLdap.MdbBaseDn) + 1;
            }
        }

        mdbDn = GetNewDn(tmpDn);
    } else {
        /* already relative */
        mdbDn = GetNewDn(Object);
    }

    return mdbDn;
}

/**
 * Get the LDAP distinguished name.  Recursively search the local cache for
 * each portion of the DN and update changed/missing parts as they are 
 * encountered.
 *
 * MdbDn must be an absolute MDB path!
 */
static char *
MdbToLdap(char *MdbDn)
{
    char *path = NULL;
    char *obj = NULL;
    char *base = NULL;
    char *attrs[] = { "dn", NULL };
    char *dn = NULL;
    char **expdn = NULL;
    int err;
    int scope;
    LDAPMessage *res = NULL;
    LDAPMessage *entry = NULL;

    XplRWReadLockAcquire(&(MdbLdap.PathLock));
    path = (char *) BongoHashtableGet(MdbLdap.PathTable, MdbDn);
    XplRWReadLockRelease(&(MdbLdap.PathLock));

    if (!path) {
        obj = strrchr(MdbDn, '\\');

        if (obj) {
            if (obj == MdbDn) {
                *obj++ = '\0';
                base = "";
                scope = LDAP_SCOPE_BASE;
            } else {
                *obj++ = '\0';
                base = MdbToLdap(MdbDn);
                scope = LDAP_SCOPE_ONELEVEL;
            }

            if (base) {
                XplMutexLock(MdbLdap.LdapMutex);
                err = ldap_search_s(MdbLdap.Ldap, base, scope, 
                                    "(objectClass=*)", attrs, 0, &res);

                if (err == LDAP_SUCCESS) {
                    entry = ldap_first_entry(MdbLdap.Ldap, res);

                    while (!path && entry) {
                        dn = ldap_get_dn(MdbLdap.Ldap, entry);

                        if (dn) {
                            expdn = ldap_explode_dn(dn, 1);

                            if (expdn && expdn[0]) {
                                /* Some directories allow multiple entries at 
                                   the same level that have the same name but 
                                   different naming types.  This comparison 
                                   will not honor this case as it stops 
                                   searching when it finds the first entry by 
                                   the same name.  This is a limitation of the 
                                   MDB API and can only be fixed by 
                                   introducing naming types into MDB or by 
                                   restricting use of the directory. */
                                if (!strcasecmp(expdn[0], obj)) {
                                    path = strdup(dn);
                                }

                                ldap_value_free(expdn);
                            }
                            /*
                            else {
                                ldap_get_option(MdbLdap.Ldap, 
                                                LDAP_OPT_ERROR_NUMBER, &err);
                                Log(LOG_ERROR, "ldap_explode_dn() failed: %s", 
                                    ldap_err2string(err));
                                Log(LOG_ERROR, "dn: %s", base);
                                break;
                            }
                            */

                            ldap_memfree(dn);
                        } else {
                            ldap_get_option(MdbLdap.Ldap, 
                                            LDAP_OPT_ERROR_NUMBER, &err);
                            Log(LOG_ERROR, "ldap_get_dn() failed: %s",
                                ldap_err2string(err));
                            Log(LOG_ERROR, "dn: %s", base);
                            break;
                        }

                        entry = ldap_next_entry(MdbLdap.Ldap, entry);
                    }

                    ldap_msgfree(res);
                } else {
                    Log(LOG_ERROR, "ldap_search_s() failed: %s",
                        ldap_err2string(err));
                    Log(LOG_ERROR, "dn: %s", base);
                }

                XplMutexUnlock(MdbLdap.LdapMutex);

                if (*base != '\0') {
                   free(base);
                }
            }

            /* not sure if this is necessary */
            obj--;
            *obj = '\\';
        }

        if (path) {
            char *mdbDn;
            char *ldapDn;

            XplRWReadLockAcquire(&(MdbLdap.PathLock));
            
            if (!(ldapDn = BongoHashtableGet(MdbLdap.PathTable, MdbDn))) {
                XplRWReadLockRelease(&(MdbLdap.PathLock));
                mdbDn = strdup(MdbDn);

                if (mdbDn) {
                    XplRWWriteLockAcquire(&(MdbLdap.PathLock));

                    if (BongoHashtablePut(MdbLdap.PathTable, mdbDn, path)) {
                        free(mdbDn);
                        free(path);
                        path = NULL;
                    }

                    XplRWWriteLockRelease(&(MdbLdap.PathLock));
                } else {
                    free(path);
                    path = NULL;
                }
            } else {
                XplRWReadLockRelease(&(MdbLdap.PathLock));
                free(path);
                path = ldapDn;
            }
        }
    }

    if (path) {
        return strdup(path);
    }

    return NULL;
}

static char *
LdapToMdb(const char *LdapDn)
{
    char *mdbDn = NULL;
    char **expdn = NULL;
    int i;

    /* this will only work if LDAP DNs are always smaller than MDB DNs */
    mdbDn = malloc(MDB_MAX_OBJECT_CHARS + 1);

    if (mdbDn) {
        *mdbDn = '\0';
        expdn = ldap_explode_dn(LdapDn, 1);

        if (expdn) {
            for (i = 0; expdn[i]; i++);

            for (i--; i >= 0; i--) {
                strcat(mdbDn, "\\");
                strcat(mdbDn, expdn[i]);
            }
                
            ldap_value_free(expdn);
        } else {
            free(mdbDn);
            mdbDn = NULL;
        }
    }

    return mdbDn;
}

static char *
LdapToAbsoluteMdb(const char *LdapDn, const char *Base)
{
    char *mdbDn;
    char *absDn = NULL;

    mdbDn = LdapToMdb(LdapDn);

    if (mdbDn) {
        absDn = GetAbsoluteDn(mdbDn, Base);
    }

    return absDn;
}

static char *
LdapToRelativeMdb(const char *LdapDn, const char *Base)
{
    char *mdbDn;
    char *relDn = NULL;

    mdbDn = LdapToMdb(LdapDn);

    if (mdbDn) {
        relDn = GetRelativeDn(mdbDn, Base);
        free(mdbDn);
    }

    return relDn;
}

static char *
SafeMdbToLdap(const char *MdbDn, const char *Base)
{
    char *absDn;
    char *ldapDn = NULL;

    absDn = GetAbsoluteDn(MdbDn, Base);

    if (absDn) {
        ldapDn = MdbToLdap(absDn);
        free(absDn);
    }

    return ldapDn;
}

static char *
NewSafeMdbToLdap(const char *MdbDn, char *Type, char *Base)
{
    char *newLeaf;
    char *mdbContextDn;
    char *ldapContextDn;
    char *ldapDn = NULL;
    int len;

    newLeaf = strrchr(MdbDn, '\\');
    if (newLeaf) {
        len = newLeaf - MdbDn;
        newLeaf++;
        mdbContextDn = malloc(len + 1);
        if (mdbContextDn) {
            memcpy(mdbContextDn, MdbDn, len);
            mdbContextDn[len] = '\0';
            ldapContextDn = SafeMdbToLdap(mdbContextDn, Base);
            free(mdbContextDn);
        } else {
            ldapContextDn = NULL;
        }
    } else {
        ldapContextDn = SafeMdbToLdap("", Base);
        newLeaf = (char *)MdbDn;                            
    }

    if (ldapContextDn) {
        ldapDn = malloc(strlen(Type) + strlen(newLeaf) + strlen(ldapContextDn) + 3);
        if (ldapDn) {
            sprintf(ldapDn, "%s=%s,%s", Type, newLeaf, ldapContextDn);
        }
        free(ldapContextDn);
    }
    return ldapDn;
}

/**
 * Returns true if the extension is supported by the server.
 */
static BOOL
LdapExtension(const char *Id)
{
    if (BongoHashtableGet(MdbLdap.ExtTable, Id)) {
        return TRUE;
    }

    return FALSE;
}

static char *
LdapSearchFilter (MDBValueStruct *types,
                  const char *pattern,
                  MDBValueStruct *attributes)
{
    char *typesPart;
    char *attributesPart;
    unsigned int i;
    StringBuilder sb = { 0, };

    AppendString(&sb, "(&(|");
    
    for (i = 0; i < types->Used; i++) {
        AppendString(&sb, "(objectclass=");
        AppendString(&sb, TranslateSchemaName(types->Value[i]));
        AppendString(&sb, ")");
    }
    
    AppendString(&sb, ")(|");
    
    for (i = 0; i < attributes->Used; i++) {
        AppendString(&sb, "(");
        AppendString(&sb, TranslateSchemaName(attributes->Value[i]));
        AppendString(&sb, "=");
        AppendString(&sb, pattern);
        AppendString(&sb, ")");
    }
    
    AppendString(&sb, "))");
    
    return sb.str;
}

/**
 * Create an LDAP filter based on object class and pattern.
 */
static char *
LdapFilter(const char *type, const char *pattern)
{
    static const char *format = "(&(objectClass=%s)(|(cn=%s)(o=%s)(ou=%s)(dc=%s)(l=%s)(c=%s)))";
    pattern = pattern ? pattern : "*";
    type = type ? type : "*";
    return sprintf_dup (format, type, 
                        pattern, pattern, pattern, pattern, pattern, pattern);
}

/**
 * Read values from the directory using ldap_search_s().
 *
 * Base must be a valid LDAP DN.
 */
static char **
LdapRead(const char *Base, const char *Attr, LDAP *Ldap)
{
    char *atmp[2] = {NULL, NULL};
    char **attrs = NULL;
    char **vals = NULL;
    char **tmps;
    LDAPMessage *res;
    LDAPMessage *entry;
    int err;

    if (!Ldap) {
        return NULL;
    }

    if (Attr) {
        attrs = atmp;
        attrs[0] = (char *)Attr;
    }

    err = ldap_search_s(Ldap, Base, LDAP_SCOPE_BASE, "(objectClass=*)", attrs,
                        0, &res);

    if (err == LDAP_SUCCESS) {
        entry = ldap_first_entry(Ldap, res);

        if (entry) {
            tmps = ldap_get_values(Ldap, entry, Attr);

            if (tmps) {
                vals = CopyValArray(tmps);
                ldap_value_free(tmps);
            } else {
                ldap_get_option(Ldap, LDAP_OPT_ERROR_NUMBER, &err);
                
                /* decoding errors occur frequently for empty attrs */
                if (err != LDAP_DECODING_ERROR) {
                    Log(LOG_ERROR, "ldap_get_values() failed: %s",
                        ldap_err2string(err));
                    Log(LOG_ERROR, "dn: %s", Base);
                    Log(LOG_ERROR, "attribute: %s", Attr);
                }
            }
        } else {
            ldap_get_option(Ldap, LDAP_OPT_ERROR_NUMBER, &err);
            Log(LOG_ERROR, "ldap_first_entry() failed: %s",
                ldap_err2string(err));
            Log(LOG_ERROR, "dn: %s", Base);
            Log(LOG_ERROR, "attribute: %s", Attr);
        }

        ldap_msgfree(res);
    } else {
        Log(LOG_ERROR, "ldap_search_s() failed: %s", ldap_err2string(err));
        Log(LOG_ERROR, "dn: %s", Base);
        Log(LOG_ERROR, "attribute: %s", Attr);
    }

    return vals;
}

static BOOL
LdapWrite(const char *Base, const char *Attr, char **Vals, int Op, 
          LDAP *Ldap)
{
    LDAPMod mod;
    LDAPMod *mods[2] = {&mod, NULL};
    int err, i, count;

    if (!Ldap) {
        return FALSE;
    }

    mod.mod_op = Op;
    mod.mod_type = (char *)Attr;
    mod.mod_values = Vals;

    err = ldap_modify_s(Ldap, Base, mods);

    if (err == LDAP_SUCCESS) {
        return TRUE;
    }

    Log(LOG_ERROR, "ldap_modify_s() failed: %s", ldap_err2string(err));
    Log(LOG_ERROR, "dn: %s", Base);
    Log(LOG_ERROR, "attribute: %s", Attr);

    if (Vals) {
        count = CountValArray(Vals);

        for (i = 0; i < count; i++) {
            Log(LOG_ERROR, "%s: %s", Attr, Vals[i]);
        }
    }

    return FALSE;
}

static BOOL
LdapCreate(const char *Base, char **Classes, BongoHashtable *Attrs, LDAP *Ldap)
{
    LDAPMod **mods, *mod, classMod, namingMod;
    char *namingVals[2], *namingType, *name = NULL, *ptr;
    BOOL result = FALSE;
    BongoHashtableIter iter;
    int i, j, next = 0;
    int err;

    if (!Ldap) {
        return FALSE;
    }

    namingType = strdup(Base);

    if (namingType) {
        ptr = strchr(namingType, '=');
        if (ptr) {
            *ptr++ = '\0';
            name = ptr;
            ptr = strchr(name, ',');
            if (ptr) {
                *ptr = '\0';
            }
        }
    }

    mods = malloc(sizeof(LDAPMod *) * (BongoHashtableSize(Attrs) + 3));

    if (mods && namingType && name) {
        namingMod.mod_op = LDAP_MOD_ADD;
        namingMod.mod_type = namingType;

        namingMod.mod_values = namingVals;
        namingMod.mod_values[0] = name;
        namingMod.mod_values[1] = NULL;

        classMod.mod_op = LDAP_MOD_ADD;
        classMod.mod_type = "objectClass";
        classMod.mod_values = Classes;

        for (BongoHashtableIterFirst(Attrs, &iter); iter.key;
             BongoHashtableIterNext(Attrs, &iter)) {
            mod = malloc(sizeof(LDAPMod));

            if (!mod) {
                break;
            }

            memset(mod, 0, sizeof(LDAPMod));
            mod->mod_op = LDAP_MOD_ADD;
            mod->mod_type = iter.key;
            mod->mod_values = iter.value;
            mods[next++] = mod;
        }

        if (next == BongoHashtableSize(Attrs)) {
            mods[next] = &classMod;
            mods[next + 1] = &namingMod;
            mods[next + 2] = NULL;

            err = ldap_add_s(Ldap, Base, mods);

            if (err == LDAP_SUCCESS) {
                result = TRUE;
            } else {
                Log(LOG_ERROR, "ldap_add_s() failed: %s", 
                    ldap_err2string(err));
                Log(LOG_ERROR, "dn: %s", Base);
                for (i = 0; mods[i]; i++) {
                    for (j = 0; mods[i]->mod_values[j]; j++) {
                        Log(LOG_ERROR, "%s: %s", mods[i]->mod_type, 
                            mods[i]->mod_values[j]);
                    }
                }
            }
        }

        for (i = 0; i < next; i++) {
            free(mods[i]);
        }

        free(namingType);
        free(mods);
    }

    return result;
}

static char **
LdapSearch(const char *Base, const char *Filter, int depth, int max, LDAP *Ldap)
{
    char *dn;
    char **paths = NULL;
    char *attrs[2] = {"dn", NULL};
    LDAPMessage *res;
    LDAPMessage *entry;
    int err;
    int count;
    int i = 0;

    if (!Ldap) {
        return NULL;
    }

    err = ldap_search_s(Ldap, Base, LDAP_SCOPE_ONELEVEL, Filter, attrs, 0, 
                        &res);

    if (err == LDAP_SUCCESS) {
        count = ldap_count_entries(Ldap, res);
        
        if (max != -1 && count > max) {
            count = max;
        }

        if (count) {
            paths = NewValArray(count);

            if (paths) {
                entry = ldap_first_entry(Ldap, res);

                while (entry) {
                    dn = ldap_get_dn(Ldap, entry);
                    
                    if (dn) {
                        paths[i] = strdup(dn);
                        ldap_memfree(dn);
                    }

                    if (!paths[i]) {
                        break;
                    }

                    i++;
                    entry = ldap_next_entry(Ldap, entry);
                }

                if (i < count) {
                    FreeValArray(paths);
                    free(paths);
                    paths = NULL;
                }
            }
        }

        ldap_msgfree(res);
    } else {
        Log(LOG_ERROR, "ldap_search_s() failed: %s", ldap_err2string(err));
        Log(LOG_ERROR, "dn: %s", Base);
    }

    return paths;
}

/**
 * Get list of object attributes.
 */
static char **
LdapAttributes(const char *Base, LDAP *Ldap)
{
    char **vals = NULL;
    LDAPMessage *res;
    LDAPMessage *entry;
    int err = 0;
    char *attr;
    BerElement *ber;
    int inc = 12;
    int max = inc;
    int i = 0;

    if (!Ldap) {
        return NULL;
    }

    err = ldap_search_s(Ldap, Base, LDAP_SCOPE_BASE, "(objectClass=*)", NULL,
                        0, &res);

    if (err == LDAP_SUCCESS) {
        entry = ldap_first_entry(Ldap, res);

        if (entry) {
            vals = NewValArray(max);

            if (vals) {
                attr = ldap_first_attribute(Ldap, entry, &ber);
                err = 0;

                while (attr) {
                    if (i == max) {
                        max += inc;

                        if (!ExpandValArray(&vals, max)) {
                            err = 1;
                            break;
                        }
                    }

                    vals[i] = strdup(attr);

                    if (!vals[i]) {
                        err = 1;
                        break;
                    }

                    i++;
                    attr = ldap_next_attribute(Ldap, entry, ber);
                }

                if (err) {
                    FreeValArray(vals);
                    free(vals);
                    vals = NULL;
                }
            }
        } else {
            ldap_get_option(Ldap, LDAP_OPT_ERROR_NUMBER, &err);
            Log(LOG_ERROR, "ldap_first_entry() failed: %s",
                ldap_err2string(err));
            Log(LOG_ERROR, "dn: %s", Base);
        }

        ldap_msgfree(res);
    } else {
        Log(LOG_ERROR, "ldap_search_s() failed: %s", ldap_err2string(err));
        Log(LOG_ERROR, "dn: %s", Base);
    }

    return vals;
}

/**
 * Verify object password.
 */
static BOOL
LdapVerify(const char *Base, const char *Password)
{
    LDAP *ldap;
    int err, tries;
    BOOL result = FALSE;

#ifdef _SOLARIS_SDK
    if (MdbLdap.LdapTls) {
        err = ldapssl_client_init(NULL, NULL);
        ldap = ldap_init(MdbLdap.LdapHostAddress, MdbLdap.LdapHostPort);
        if (ldap) {
            if (err == LDAP_SUCCESS) {
                err = ldapssl_install_routines(ldap);
            }
            if (err == LDAP_SUCCESS) {
#ifdef LDAP_OPT_SSL
                err = ldap_set_option(ldap, LDAP_OPT_SSL, LDAP_OPT_ON);
#endif
                ldap_set_option(ldap, LDAP_OPT_RECONNECT, LDAP_OPT_ON);
            }
            if (err != LDAP_SUCCESS) {
               Log(LOG_ERROR, "Enabling TLS failed: %s",
                   ldap_err2string(err));
               ldap_unbind_ext(ldap, NULL, NULL);
               ldap = NULL;
           }
        }
     } else {
          ldap = ldap_init(MdbLdap.LdapHostAddress, MdbLdap.LdapHostPort);
     }
#else
    ldap = ldap_init(MdbLdap.LdapHostAddress, MdbLdap.LdapHostPort);
    if (ldap) {
        if (MdbLdap.LdapTls) {
            err = ldap_start_tls_s(ldap, NULL, NULL);
            if (err != LDAP_SUCCESS) {
                Log(LOG_ERROR, "ldap_start_tls_s() failed: %s",
                    ldap_err2string(err));
                ldap_unbind_ext(ldap, NULL, NULL);
                ldap = NULL;
            }
        }
     }
#endif

   if (ldap) {

	for (tries = 0; tries < 3; tries++) {
            err = ldap_simple_bind_s(ldap, Base, Password);

            if (err == LDAP_SUCCESS) {
                result = TRUE;
                break;
            }
        }

        ldap_unbind_s(ldap);
    }

    return result;
}

/**
 * Change object password.
 */
static BOOL
LdapPassword(const char *Base, char *OldPassword, char *NewPassword, 
             LDAP *Ldap)
{
    BerElement *elem;
    struct berval *bv = NULL;
    int id = LDAP_OTHER;
    int err = LDAP_OTHER;
    char *vals[2] = {NewPassword, NULL};

    if (!Ldap) {
        return FALSE;
    }

#ifdef _SOLARIS_SDK
    {
#else
    if (LdapExtension("1.3.6.1.4.1.4203.1.11.1")) {
        /* This method uses the modify password extended operation.
           See RFC 3062 for details. */
        elem = ber_alloc_t(LBER_USE_DER);

        ber_printf(elem, "{");
        ber_printf(elem, "ts", LDAP_TAG_EXOP_MODIFY_PASSWD_ID, Base);

        if (*OldPassword != '\0') {
            ber_printf(elem, "ts", LDAP_TAG_EXOP_MODIFY_PASSWD_OLD, 
                       OldPassword);
        }

        ber_printf(elem, "ts", LDAP_TAG_EXOP_MODIFY_PASSWD_NEW, NewPassword);
        ber_printf(elem, "N}");

        err = ber_flatten(elem, &bv);

        if (err < 0 || !bv->bv_val) {
            ber_free(elem, 1);
            return FALSE;
        }

        err = ldap_extended_operation(Ldap, LDAP_EXOP_MODIFY_PASSWD, bv, NULL, 
                                      NULL, &id);
        ber_free(elem, 1);
        ber_bvfree(bv);

        if (err == LDAP_SUCCESS) {
            return TRUE;
        }
#if 0
/* So far I have been unsuccessful in getting this to work on 
   Active Directory.  Also, the BASE64 encoder inserts line breaks, which is
   a potential problem if your password is long enough. */
    } else if (BongoHashtableGet(MdbLdap.AttrTable, "unicodePwd")) {
        /* Jumping through hoops for Active Directory... */
        /* This will fail without a secure connection. */
        LDAPMod modNewPassword;
        LDAPMod modOldPassword;
        LDAPMod *modEntry[3] = {NULL, NULL, NULL};
        struct berval newPwdBerVal;
        struct berval oldPwdBerVal;
        struct berval *newPwd_attr[2] = {&newPwdBerVal, NULL};
        struct berval *oldPwd_attr[2] = {&oldPwdBerVal, NULL};
        char newPwdQuoted[1024];
        char oldPwdQuoted[1024];
        char newPwdEncoded[1024];
        char oldPwdEncoded[1024];
        char tmp[1024];
        int count = 0;
        char *codecs[2] = {"UCS-2", "BASE64"};
        BongoStream *stream = BongoStreamCreate(codecs, 2, TRUE);

        if (stream) {
            sprintf(newPwdQuoted, "\"%s\"", NewPassword);
            BongoStreamPut(stream, newPwdQuoted, strlen(newPwdQuoted));
            BongoStreamEos(stream);
            count = BongoStreamGet(stream, newPwdEncoded, 1024);

            if (count == 1024 || BongoStreamGet(stream, tmp, 1024)) {
                BongoStreamFree(stream);
                Log(LOG_ERROR, "Encoding error: %s", newPwdEncoded);
                return FALSE;
            }

            newPwdEncoded[count - 2] = '\0';
            newPwdBerVal.bv_len = strlen(newPwdEncoded);
            newPwdBerVal.bv_val = (char *) newPwdEncoded;
            modNewPassword.mod_type = "unicodePwd";
            modNewPassword.mod_vals.modv_bvals = newPwd_attr;

            if (OldPassword && OldPassword[0]) {
                sprintf(oldPwdQuoted, "\"%s\"", OldPassword);
                BongoStreamPut(stream, oldPwdQuoted, strlen(oldPwdQuoted));
                BongoStreamEos(stream);
                count = BongoStreamGet(stream, oldPwdEncoded, 1024);

                if (count == 1024 || BongoStreamGet(stream, tmp, 1024)) {
                    BongoStreamFree(stream);
                    Log(LOG_ERROR, "Encoding error: %s", oldPwdEncoded);
                    return FALSE;
                }

                oldPwdEncoded[count - 2] = '\0';
                oldPwdBerVal.bv_len = strlen(oldPwdEncoded);
                oldPwdBerVal.bv_val = (char *) oldPwdEncoded;
                modOldPassword.mod_op = LDAP_MOD_DELETE | LDAP_MOD_BVALUES;
                modOldPassword.mod_type = "unicodePwd";
                modOldPassword.mod_vals.modv_bvals = oldPwd_attr;
                modNewPassword.mod_op = LDAP_MOD_ADD | LDAP_MOD_BVALUES;
                modEntry[0] = &modOldPassword;
                modEntry[1] = &modNewPassword;
            } else {
                modNewPassword.mod_op = LDAP_MOD_REPLACE | LDAP_MOD_BVALUES;
                modEntry[0] = &modNewPassword;
            }

            err = ldap_modify_s(Ldap, Base, modEntry);

            if (err != LDAP_SUCCESS) {
                Log(LOG_ERROR, "ldap_modify_s() failed: %s", ldap_err2string(err));
                Log(LOG_ERROR, "dn: %s", Base);
                Log(LOG_ERROR, "attribute: %s", modNewPassword.mod_type);
                Log(LOG_ERROR, "value: %s", newPwdEncoded);
                return FALSE;
            }

            BongoStreamFree(stream);
        }

        return TRUE;
#endif
    } else {
#endif
        /* This is the old-school method.
           Hopefully the directory performs some sort of encryption for us. */
        if (OldPassword && OldPassword[0] && !LdapVerify(Base, OldPassword)) {
            return FALSE;
        }

        return LdapWrite(Base, "userPassword", vals, LDAP_MOD_REPLACE, Ldap);
    }

    return FALSE;
}

static LDAP *
InitLdapConnection(char *BindDn, char *Passwd, void *rebind_proc, 
                   MDBLDAPContextStruct *Context)
{
    LDAP *ldap = NULL;
    int err, tries;

#ifdef _SOLARIS_SDK
    if (MdbLdap.LdapTls) {
        err = ldapssl_client_init(NULL, NULL);
        ldap = ldap_init(MdbLdap.LdapHostAddress, MdbLdap.LdapHostPort);
        if (ldap) {
            if (err == LDAP_SUCCESS) {
                err = ldapssl_install_routines(ldap);
            }
            if (err == LDAP_SUCCESS) {
#ifdef LDAP_OPT_SSL
                err = ldap_set_option(ldap, LDAP_OPT_SSL, LDAP_OPT_ON);
#endif
                ldap_set_option(ldap, LDAP_OPT_RECONNECT, LDAP_OPT_ON);
            }
            if (err != LDAP_SUCCESS) {
               Log(LOG_ERROR, "Enabling TLS failed: %s",
                   ldap_err2string(err));
               ldap_unbind_ext(ldap, NULL, NULL);
               ldap = NULL;
           }
        }
     } else {
          ldap = ldap_init(MdbLdap.LdapHostAddress, MdbLdap.LdapHostPort);
     }
#else
    ldap = ldap_init(MdbLdap.LdapHostAddress, MdbLdap.LdapHostPort);
    if (ldap) {
        if (MdbLdap.LdapTls) {
            err = ldap_start_tls_s(ldap, NULL, NULL);
            if (err != LDAP_SUCCESS) {
                Log(LOG_ERROR, "ldap_start_tls_s() failed: %s",
                    ldap_err2string(err));
                ldap_unbind_ext(ldap, NULL, NULL);
                ldap = NULL;
            }
        }
     }
#endif
    if (ldap) {
	for (tries = 0; tries < 3; tries++) {
            err = ldap_simple_bind_s(ldap, BindDn, Passwd);

            if (err == LDAP_SUCCESS) {
                break;
            }
        }

        if (err == LDAP_SUCCESS) {
            ldap_set_rebind_proc(ldap, rebind_proc, Context);
        } else {
            Log(LOG_ERROR, "ldap_simple_bind_s() failed: %s",
                ldap_err2string(err));
            Log(LOG_ERROR, "dn: %s", BindDn);
            ldap_unbind_s(ldap);
            ldap = NULL;
        }
    } else {
        Log(LOG_ERROR, "ldap_init() failed: %s", ldap_err2string(errno));
        Log(LOG_ERROR, "host: %s:%d", MdbLdap.LdapHostAddress, 
            MdbLdap.LdapHostPort);
    }

    return ldap;
}

static BOOL
DeleteLdapConnection(LDAP *Ldap)
{
    int err;

    if (Ldap) {
        err = ldap_unbind_s(Ldap);

        if (err != LDAP_SUCCESS) {
            Log(LOG_ERROR, "ldap_unbind() failed: %s", ldap_err2string(err));
            return FALSE;
        }
    }

    return TRUE;
}

static LDAP *
GetCachedLdapConnection(MDBLDAPContextStruct *Context)
{
    LDAP *ldap = NULL;

    if (Context->conn.min) {
        XplMutexLock(Context->conn.mutex);

        if (Context->conn.next > -1) {
            ldap = Context->conn.list[Context->conn.next--];
        }

        XplMutexUnlock(Context->conn.mutex);
    }

    return ldap;
}

static BOOL
AddCachedLdapConnection(MDBLDAPContextStruct *Context, LDAP *Ldap)
{
    BOOL result = FALSE;

    if (Context->conn.min) {
        XplMutexLock(Context->conn.mutex);

        if (Context->conn.next + 1 < Context->conn.max) {
            Context->conn.list[++Context->conn.next] = Ldap;
            result = TRUE;
        }

        XplMutexUnlock(Context->conn.mutex);
    }

    return result;
}

static LDAP *
GetLdapConnection(MDBLDAPContextStruct *Context)
{
    LDAP *ldap = NULL;
    
    ldap = GetCachedLdapConnection(Context);

    if (!ldap) {
        ldap = InitLdapConnection(Context->binddn, Context->passwd, 
                                  UserRebindProc, Context);
    }

    return ldap;
}

static BOOL
ReleaseLdapConnection(MDBLDAPContextStruct *Context, LDAP *Ldap)
{
    if (!AddCachedLdapConnection(Context, Ldap)) {
        return DeleteLdapConnection(Ldap);
    }

    return TRUE;
}

static void
DestroyContext(MDBLDAPContextStruct *context)
{
    if (context->binddn) {
        free(context->binddn);
    }

    if (context->passwd) {
        free(context->passwd);
    }

    XplMutexDestroy(context->conn.mutex);

    if (context->conn.list) {
        /* clean up connection pool */
        while (context->conn.next >= 0) {
            DeleteLdapConnection(context->conn.list[context->conn.next--]);
        }

        free(context->conn.list);
    }

    free(context);
}

static MDBLDAPContextStruct *
CreateContext(const char *BindDn, const char *Password)
{
    MDBLDAPContextStruct *context;
    char *ldapDn;

    context = malloc(sizeof(MDBLDAPContextStruct));

    if (context) {
        memset(context, 0, sizeof(MDBLDAPContextStruct));

        if (BindDn && Password) {
            ldapDn = SafeMdbToLdap(BindDn, NULL);

            if (ldapDn) {
                context->binddn = ldapDn;
                context->passwd = strdup(Password);

                if (!context->passwd) {
                    free(context->binddn);
                    free(context);
                    context = NULL;
                }
            } else {
                free(context);
                context = NULL;
            }
        }

        if (context) {
            context->conn.next = -1;
            context->conn.min = MdbLdap.LdapMinConnections;
            context->conn.max = MdbLdap.LdapMaxConnections;
            XplMutexInit(context->conn.mutex);

            /* initialize the connection pool */
            context->conn.list = malloc(sizeof(LDAP *) * context->conn.max);

            if (context->conn.list) {
                if (context->conn.min) {
                    /* don't init connections if min is 0 */
                    while (context->conn.next < context->conn.min) {
                        context->conn.list[++context->conn.next] = 
                            InitLdapConnection(context->binddn, 
                                               context->passwd, 
                                               UserRebindProc, context);

                        if (!context->conn.list[context->conn.next]) {
                            DestroyContext(context);
                            context = NULL;
                            break;
                        }
                    }
                }
            } else {
                DestroyContext(context);
                context = NULL;
            }
        }
    }

    return context;
}

/**
 * Functions for handling the attribute schema cache.
 */

static void
AttrDel(void *value)
{
    MDBLDAPSchemaAttr *attr = (MDBLDAPSchemaAttr *) value;

    if (attr->refcnt > 1) {
        attr->refcnt--;
    } else {
        free(attr->val);

        if (attr->names) {
            free(attr->names);
        }

        free(attr);
    }
}

static void
AttrUpdate(char *key, MDBLDAPSchemaAttr *attr, void *data)
{
    MDBLDAPSchemaAttr *ptr = NULL;

    /* fill out incomplete properties on schema attributes by following
       superclass hierarchy */

    if (!attr->visited && attr->sup) {
        ptr = BongoHashtableGet(MdbLdap.AttrTable, attr->sup);

        if (!ptr->visited) {
            AttrUpdate(attr->sup, ptr, NULL);
        }

        if (!attr->desc && ptr->desc) {
            attr->desc = ptr->desc;
        }

        if (!attr->equality && ptr->equality) {
            attr->equality = ptr->equality;
        }

        if (!attr->syntax && ptr->syntax) {
            attr->syntax = ptr->syntax;
        }

        if (!attr->singleval && ptr->singleval) {
            attr->singleval = ptr->singleval;
        }

        if (ptr->sup) {
            ptr = BongoHashtableGet(MdbLdap.AttrTable, ptr->sup);
        } else {
            ptr = NULL;
        }
    }

    attr->visited = TRUE;
}

#if 0
static void
AttrPrint(void *key, void *value, void *data)
{
    MDBLDAPSchemaAttr *attr = (MDBLDAPSchemaAttr *) value;
    printf("%s: %s\n", key, attr->equality);
}
#endif

/**
 * Read LDAPv3 supported extensions from root
 * We use this to quickly determine what the server supports when using LDAP
 * functions (especially extended operations).
 */
static BOOL
LoadExtensions(void)
{
    char **vals;
    char *ext;
    int i;
    BOOL result = TRUE;

    XplMutexLock(MdbLdap.LdapMutex);
    vals = LdapRead("", "supportedExtension", MdbLdap.Ldap);
    XplMutexUnlock(MdbLdap.LdapMutex);

    if (vals) {
        for (i = 0; vals[i]; i++) {
            ext = strdup(vals[i]);

            if (ext) {
                if (BongoHashtablePut(MdbLdap.ExtTable, ext, ext)) {
                    result = FALSE;
                    free(ext);
                    break;
                }
            } else {
                result = FALSE;
                break;
            }
        }

        FreeValArray(vals);
        free(vals);
    }

    return result;
}

/**
 * Read LDAPv3 attribute schema definitions from SubschemaSubentry object.
 */
static BOOL
LoadSchemaAttrs(void)
{
    char **vals;
    int i, j;
    MDBLDAPSchemaAttr *attr;
    BOOL result = TRUE;

    XplMutexLock(MdbLdap.LdapMutex);
    vals = LdapRead(MdbLdap.LdapSchemaDn, "attributeTypes", MdbLdap.Ldap);
    XplMutexUnlock(MdbLdap.LdapMutex);

    if (vals) {
        for (i = 0; vals[i]; i++) {
            attr = malloc(sizeof(MDBLDAPSchemaAttr));

            if (attr) {
                memset(attr, 0, sizeof(MDBLDAPSchemaAttr));
                attr->val = vals[i];
                result = ParseAttributeTypeDescription(vals[i], attr);

                if (result && attr->names && attr->names[0]) {
                    for (j = 0; attr->names[j]; j++) {
                        BongoHashtablePut(MdbLdap.AttrTable, attr->names[j],
                                         attr);
                        /*XplConsolePrintf("loading attribute: >>%s<<\n", attr->names[j]);*/
                        attr->refcnt++;
                    }
                } else {
                    result = FALSE;
                    break;
                }
            } else {
                result = FALSE;
            }
        }

        if (result) {
            BongoHashtableForeach(MdbLdap.AttrTable, (HashtableForeachFunc)AttrUpdate, NULL);
        }

        free(vals);
    } else {
        result = FALSE;
    }

    return result;
}


/** 
 * Functions for handling the class schema cache.
 */

static void
ClassDel(void *value)
{
    MDBLDAPSchemaClass *class = (MDBLDAPSchemaClass *) value;

    if (class->refcnt > 1) {
        class->refcnt--;
    } else {
        free(class->val);

        if (class->names) {
            free(class->names);
        }

        if (class->sup) {
            free(class->sup);
        }

        if (class->must) {
            free(class->must);
        }

        if (class->may) {
            free(class->may);
        }

        free(class);
    }
}

static void
ClassUpdate(char *key, MDBLDAPSchemaClass *class, void *data)
{
    MDBLDAPSchemaClass *ptr = NULL;
    int count;
    int inc = 4;
    int i, j;

    if (!class->visited && class->sup) {
        for (j = 0; class->sup[j]; j++) {
            ptr = BongoHashtableGet(MdbLdap.ClassTable, class->sup[j]);

            if (!ptr->visited) {
                ClassUpdate(class->sup[j], ptr, NULL);
            }

            if (!class->desc && ptr->desc) {
                class->desc = ptr->desc;
            }

            if (ptr->must) {
                if (!class->must) {
                    class->must = NewValArray(inc);
                }

                count = CountValArray(class->must);

                for (i = 0; ptr->must[i]; i++) {
                    if (!(count % inc)) {
                        if (!ExpandValArray(&(class->must), count + inc)) {
                            return;
                        }
                    }

                    class->must[count++] = ptr->must[i];
                }
            }

            if (ptr->may) {
                if (!class->may) {
                    class->may = NewValArray(inc);
                }

                count = CountValArray(class->may);

                for (i = 0; ptr->may[i]; i++) {
                    if (!(count % inc)) {
                        if (!ExpandValArray(&(class->may), count + inc)) {
                            return;
                        }
                    }

                    class->may[count++] = ptr->may[i];
                }
            }

            /* It turns out that classes can inherit from multiple 
               super-classes..  this means we have to pick one of them for
               our naming attribute (see the 'HATE LDAP!!' comment below). 
               This method will use the last super-class. */

            /* abstract superiors don't define naming attribute */
            /* look at the current class */

            class->naming = NULL;

            if (class->must) {
                for (i = 0; class->must[i]; i++) {
                    if (!strcasecmp(class->must[i], "cn")) {
                        class->naming = "cn";
                        break;
                    } else if (!strcasecmp(class->must[i], "o")) {
                        class->naming = "o";
                        break;
                    } else if (!strcasecmp(class->must[i], "ou")) {
                        class->naming = "ou";
                        break;
                    } else if (!strcasecmp(class->must[i], "c")) {
                        class->naming = "c";
                        break;
                    } else if (!strcasecmp(class->must[i], "dc")) {
                        class->naming = "dc";
                        break;
                    } else if (!strcasecmp(class->must[i], "l")) {
                        class->naming = "l";
                        break;
                    }
                }
            }

            if (!class->naming && strcmp(ptr->naming, "cn")) {
                class->naming = ptr->naming;
            }

            if (!class->naming && class->may) {
                for (i = 0; class->may[i]; i++) {
                    if (!strcasecmp(class->may[i], "cn")) {
                        class->naming = "cn";
                        break;
                    } else if (!strcasecmp(class->may[i], "o")) {
                        class->naming = "o";
                        break;
                    } else if (!strcasecmp(class->may[i], "ou")) {
                        class->naming = "ou";
                        break;
                    } else if (!strcasecmp(class->may[i], "c")) {
                        class->naming = "c";
                        break;
                    } else if (!strcasecmp(class->may[i], "dc")) {
                        class->naming = "dc";
                        break;
                    } else if (!strcasecmp(class->may[i], "l")) {
                        class->naming = "l";
                        break;
                    }
                }
            }

            if (!class->naming) {
                class->naming = "cn";
            }
        }
    }

    class->visited = TRUE;
}

#if 0
static void
ClassPrint(void *key, void *value, void *data)
{
    MDBLDAPSchemaClass *class = (MDBLDAPSchemaClass *) value;
    printf("%s: %s\n", key, class->naming);
}
#endif

/**
 * Read LDAPv3 class schema definitions from SubschemaSubentry object.
 */
static BOOL
LoadSchemaClasses(void)
{
    char **vals;
    int i, j;
    MDBLDAPSchemaClass *class;
    BOOL result = TRUE;

    XplMutexLock(MdbLdap.LdapMutex);
    vals = LdapRead(MdbLdap.LdapSchemaDn, "objectClasses", MdbLdap.Ldap);
    XplMutexUnlock(MdbLdap.LdapMutex);

    if (vals) {
        for (i = 0; vals[i]; i++) {
            class = malloc(sizeof(MDBLDAPSchemaClass));

            if (class) {
                memset(class, 0, sizeof(MDBLDAPSchemaClass));
                class->val = vals[i];
                result = ParseObjectClassDescription(vals[i], class);

                if (result && class->names && class->names[0]) {
                    for (j = 0; class->names[j]; j++) {
                        /* Sometimes I absolutely HATE LDAP!!!
                        There really is no defined way to determine what the 
                        naming attribute is supposed to be for a particular 
                        object.  Sometimes attributes that extend the 'name' 
                        attribute are used, sometimes attributes like 'uid' 
                        are used.  Some classes have multiple 'name' 
                        attributes and some don't have any at all. Some naming 
                        attributes are only used for certain classes. I really 
                        wish there was something defined in the schema to help 
                        solve this problem but there is _not_.  I've tried my 
                        best to keep schema definitions out of the driver but 
                        it looks like I have to hard-code some here.  If the 
                        class is not known, it will have to be added.  
                        Otherwise, all classes will default to 'cn'.*/

                        /* TODO: Maybe 'uid' would be a better default? */

                        if (!strcasecmp(class->names[j], "country")) {
                            class->naming = "c";
                        } else if (!strcasecmp(class->names[j], "dcObject")) {
                            class->naming = "dc";
                        } else if (!strcasecmp(class->names[j], "domain")) {
                            class->naming = "dc";
                        } else if (!strcasecmp(class->names[j], "locality")) {
                            class->naming = "l";
                        } else if (!strcasecmp(class->names[j], "organization")) {
                            class->naming = "o";
                        } else if (!strcasecmp(class->names[j], 
                                           "organizationalUnit")) {
                            class->naming = "ou";
                        } else {
                            class->naming = "cn";
                        }

                        BongoHashtablePut(MdbLdap.ClassTable, 
                                         class->names[j], class);
                        class->refcnt++;
                    }
                } else {
                    result = FALSE;
                    break;
                }
            } else {
                result = FALSE;
            }
        }

        if (result) {
            BongoHashtableForeach(MdbLdap.ClassTable, (HashtableForeachFunc)ClassUpdate, NULL);
        }

        free(vals);
    } else {
        result = FALSE;
    }

    return result;
}

/**
 * Make a simple translation of an xplschema.h schema name.
 * Removes illegal characters from schema name.
 */
static BOOL
RemoveIllegalChars(char *Buffer, char *Name, char *illegal)
{
    char *orig;
    char *token;
    char *ptr;

    if (strpbrk(Name, illegal)) {
        orig = strdup(Name);

        if (orig) {
            Buffer[0] = '\0';
            token = strtok_r(orig, illegal, &ptr);

            while (token) {
                strcat(Buffer, token);
                token = strtok_r(NULL, illegal, &ptr);
            }
            
            free(orig);
        } else {
            return FALSE;
        }
    } else {
        strcpy(Buffer, Name);
    }

    return TRUE;
}

static BOOL
AddTranslation(char *Orig, char *Trans)
{
    char *orig = strdup(Orig);
    char *trans = strdup(Trans);

    if (orig && trans) {
        if (BongoHashtablePut(MdbLdap.TransTable, trans, orig)) {
            free(orig);
            free(trans);
            return FALSE;
        }
    } else {
        if (orig) {
            free(orig);
        }

        return FALSE;
    }

    return TRUE;
}

static BOOL
InitTranslations(void)
{
    /* The schema translation table:
       Should include all definitions from xplschema.h with relevant LDAP
       translations.  If translation is NULL, no specific translation is known.
       If the name contains illegal LDAP characters, a translation will be 
       generated and registered for the name. */
    char *trans_table[][2] = {
        {C_AFP_SERVER, NULL},
        {C_ALIAS, "alias"}, /* RFC2256 */
        {C_BINDERY_OBJECT, NULL},
        {C_BINDERY_QUEUE, NULL},
        {C_COMMEXEC, NULL},
        {C_COMPUTER, NULL},
        {C_COUNTRY, "country"}, /* RFC2256 */
        {C_DEVICE, "device"}, /* RFC2256 */
        {C_DIRECTORY_MAP, NULL},
        {C_EXTERNAL_ENTITY, NULL},
        {C_GROUP, "groupOfNames"}, /* RFC2256 */
        {C_LIST, NULL},
        {C_LOCALITY, "locality"}, /* RFC2256 */
        {C_MESSAGE_ROUTING_GROUP, NULL},
        {C_MESSAGING_ROUTING_GROUP, NULL},
        {C_MESSAGING_SERVER, NULL},
        {C_NCP_SERVER, NULL},
        {C_ORGANIZATION, "organization"}, /* RFC2256 */
        {C_ORGANIZATIONAL_PERSON, "organizationalPerson"}, /* RFC2256 */
        {C_ORGANIZATIONAL_ROLE, "organizationalRole"}, /* RFC2256 */
        {C_ORGANIZATIONAL_UNIT, "organizationalUnit"}, /* RFC2256 */
        {C_PARTITION, NULL},
        {C_PERSON, "person"}, /* RFC2256 */
        {C_PRINT_SERVER, NULL},
        {C_PRINTER, NULL},
        {C_PROFILE, NULL},
        {C_QUEUE, NULL},
        {C_RESOURCE, NULL},
        {C_SERVER, NULL},
        {C_TOP, "top"}, /* RFC2256 */
        {C_TREE_ROOT, NULL},
        {C_UNKNOWN, NULL},
        {C_USER, "inetOrgPerson"}, /* RFC2798 */
        {C_VOLUME, NULL},
        {A_ACCOUNT_BALANCE, NULL},
        {A_ACL, NULL},
        {A_ALIASED_OBJECT_NAME, "aliasedObjectName"}, /* RFC2256 */
        {A_ALLOW_UNLIMITED_CREDIT, NULL},
        {A_AUTHORITY_REVOCATION, NULL},
        {A_BACK_LINK, NULL},
        {A_BINDERY_OBJECT_RESTRICTION, NULL},
        {A_BINDERY_PROPERTY, NULL},
        {A_BINDERY_TYPE, NULL},
        {A_CARTRIDGE, NULL},
        {A_CA_PRIVATE_KEY, NULL},
        {A_CA_PUBLIC_KEY, NULL},
        {A_CERTIFICATE_REVOCATION, NULL},
        {A_CERTIFICATE_VALIDITY_INTERVAL, NULL},
        {A_COMMON_NAME, "cn"}, /* RFC2256 */
        {A_CONVERGENCE, NULL},
        {A_COUNTRY_NAME, "c"}, /* RFC2256 */
        {A_CROSS_CERTIFICATE_PAIR, "crossCertificatePair"}, /* RFC2256 */
        {A_DEFAULT_QUEUE, NULL},
        {A_DESCRIPTION, "description"}, /* RFC2256 */
        {A_DETECT_INTRUDER, NULL},
        {A_DEVICE, NULL},
        {A_DS_REVISION, NULL},
        {A_EMAIL_ADDRESS, "mail"}, /* RFC1274 */
        {A_EQUIVALENT_TO_ME, NULL},
        {A_EXTERNAL_NAME, NULL},
        {A_EXTERNAL_SYNCHRONIZER, NULL},
        {A_FACSIMILE_TELEPHONE_NUMBER, "facsimileTelephoneNumber"}, /*RFC2256*/
        {A_FULL_NAME, NULL}, /* displayName? */
        {A_GENERATIONAL_QUALIFIER, "generationQualifier"}, /* RFC2256 */
        {A_GID, NULL}, /* gidNumber? */
        {A_GIVEN_NAME, "givenName"}, /* RFC2256 */
        {A_GROUP_MEMBERSHIP, NULL},
        {A_HIGH_SYNC_INTERVAL, NULL},
        {A_HIGHER_PRIVILEGES, NULL},
        {A_HOME_DIRECTORY, NULL},
        {A_HOST_DEVICE, NULL},
        {A_HOST_RESOURCE_NAME, NULL},
        {A_HOST_SERVER, NULL}, /* host? */
        {A_INHERITED_ACL, NULL},
        {A_INITIALS, "initials"}, /* RFC2256 */
        {A_INTERNET_EMAIL_ADDRESS, "mail"}, /* RFC 2789 */
        {A_INTRUDER_ATTEMPT_RESET_INTRVL, NULL},
        {A_INTRUDER_LOCKOUT_RESET_INTRVL, NULL},
        {A_LOCALITY_NAME, "l"},
        {A_LANGUAGE, NULL}, /* preferredLanguage? */
        {A_LAST_LOGIN_TIME, NULL},
        {A_LAST_REFERENCED_TIME, NULL},
        {A_LOCKED_BY_INTRUDER, NULL},
        {A_LOCKOUT_AFTER_DETECTION, NULL},
        {A_LOGIN_ALLOWED_TIME_MAP, NULL},
        {A_LOGIN_DISABLED, NULL},
        {A_LOGIN_EXPIRATION_TIME, NULL},
        {A_LOGIN_GRACE_LIMIT, NULL},
        {A_LOGIN_GRACE_REMAINING, NULL},
        {A_LOGIN_INTRUDER_ADDRESS, NULL},
        {A_LOGIN_INTRUDER_ATTEMPTS, NULL},
        {A_LOGIN_INTRUDER_LIMIT, NULL},
        {A_LOGIN_INTRUDER_RESET_TIME, NULL},
        {A_LOGIN_MAXIMUM_SIMULTANEOUS, NULL},
        {A_LOGIN_SCRIPT, NULL},
        {A_LOGIN_TIME, NULL},
        {A_LOW_RESET_TIME, NULL},
        {A_LOW_SYNC_INTERVAL, NULL},
        {A_MAILBOX_ID, NULL},
        {A_MAILBOX_LOCATION, NULL},
        {A_MEMBER, "member"}, /* RFC2256 */
        {A_MEMORY, NULL},
        {A_MESSAGE_SERVER, NULL},
        {A_MESSAGE_ROUTING_GROUP, NULL},
        {A_MESSAGING_DATABASE_LOCATION, NULL},
        {A_MESSAGING_ROUTING_GROUP, NULL},
        {A_MESSAGING_SERVER, NULL},
        {A_MESSAGING_SERVER_TYPE, NULL},
        {A_MINIMUM_ACCOUNT_BALANCE, NULL},
        {A_NETWORK_ADDRESS, NULL},
        {A_NETWORK_ADDRESS_RESTRICTION, NULL},
        {A_NNS_DOMAIN, NULL},
        {A_NOTIFY, NULL},
        {A_OBITUARY, NULL},
        {A_ORGANIZATION_NAME, "o"}, /* RFC2256 */
        {A_OBJECT_CLASS, "objectClass"}, /* RFC2256 */
        {A_OPERATOR, NULL},
        {A_ORGANIZATIONAL_UNIT_NAME, "ou"}, /* RFC2256 */
        {A_OWNER, "owner"}, /* RFC2256 */
        {A_PAGE_DESCRIPTION_LANGUAGE, NULL},
        {A_PARTITION_CONTROL, NULL},
        {A_PARTITION_CREATION_TIME, NULL},
        {A_PARTITION_STATUS, NULL},
        {A_PASSWORD_ALLOW_CHANGE, NULL},
        {A_PASSWORD_EXPIRATION_INTERVAL, NULL},
        {A_PASSWORD_EXPIRATION_TIME, NULL},
        {A_PASSWORD_MANAGEMENT, NULL},
        {A_PASSWORD_MINIMUM_LENGTH, NULL},
        {A_PASSWORD_REQUIRED, NULL},
        {A_PASSWORD_UNIQUE_REQUIRED, NULL},
        {A_PASSWORDS_USED, NULL},
        {A_PATH, NULL},
        {A_PERMANENT_CONFIG_PARMS, NULL},
        {A_PHYSICAL_DELIVERY_OFFICE_NAME, "physicalDeliveryOfficeName"},
        {A_POSTAL_ADDRESS, "postalAddress"}, /* RFC2256 */
        {A_POSTAL_CODE, "postalCode"}, /* RFC2256 */
        {A_POSTAL_OFFICE_BOX, "postalOfficeBox"}, /* RFC2256 */
        {A_POSTMASTER, NULL},
        {A_PRINT_SERVER, NULL},
        {A_PRIVATE_KEY, NULL},
        {A_PRINTER, NULL},
        {A_PRINTER_CONFIGURATION, NULL},
        {A_PRINTER_CONTROL, NULL},
        {A_PRINT_JOB_CONFIGURATION, NULL},
        {A_PROFILE, NULL},
        {A_PROFILE_MEMBERSHIP, NULL},
        {A_PUBLIC_KEY, NULL},
        {A_QUEUE, NULL},
        {A_QUEUE_DIRECTORY, NULL},
        {A_RECEIVED_UP_TO, NULL},
        {A_REFERENCE, NULL},
        {A_REPLICA, NULL},
        {A_REPLICA_UP_TO, NULL},
        {A_RESOURCE, NULL},
        {A_REVISION, NULL},
        {A_ROLE_OCCUPANT, "roleOccupant"}, /* RFC2256 */
        {A_STATE_OR_PROVINCE_NAME, "st"}, /* RFC2256 */
        {A_STREET_ADDRESS, "street"}, /* RFC2256 */
        {A_SAP_NAME, NULL},
        {A_SECURITY_EQUALS, NULL},
        {A_SECURITY_FLAGS, NULL},
        {A_SEE_ALSO, "seeAlso"}, /* RFC2256 */
        {A_SERIAL_NUMBER, "serialNumber"}, /* RFC2256 */
        {A_SERVER, NULL},
        {A_SERVER_HOLDS, NULL},
        {A_STATUS, NULL},
        {A_SUPPORTED_CONNECTIONS, NULL},
        {A_SUPPORTED_GATEWAY, NULL},
        {A_SUPPORTED_SERVICES, NULL},
        {A_SUPPORTED_TYPEFACES, NULL},
        {A_SURNAME, "sn"}, /* RFC2256 */
        {A_IN_SYNC_UP_TO, NULL},
        {A_TELEPHONE_NUMBER, "telephoneNumber"}, /* RFC2256 */
        {A_TITLE, "title"}, /* RFC2256 */
        {A_TYPE_CREATOR_MAP, NULL},
        {A_UID, "uid"}, /* RFC1274 */
        {A_UNKNOWN, NULL},
        {A_UNKNOWN_BASE_CLASS, NULL},
        {A_USER, NULL},
        {A_VERSION, NULL},
        {A_VOLUME, NULL},
        {NULL, NULL}
    };
    int i;
    char *illegal = " :";
    char trans[512];

    for (i = 0; trans_table[i][0]; i++) {
        if (trans_table[i][1]) {
            if (!AddTranslation(trans_table[i][0], trans_table[i][1])) {
                return FALSE;
            }

            if (!AddTranslation(trans_table[i][1], trans_table[i][0])) {
                return FALSE;
            }
        } else {
            if (!RemoveIllegalChars(trans, trans_table[i][0], illegal)) {
                return FALSE;
            }

            if (strcmp(trans_table[i][0], trans)) {
                if (!AddTranslation(trans_table[i][0], trans)) {
                    return FALSE;
                }

                if (!AddTranslation(trans, trans_table[i][0])) {
                    return FALSE;
                }
            }
        }
    }

    return TRUE;
}

static char *
TranslateSchemaName(char *Name)
{
    char *name;

    if (Name) {
        name = BongoHashtableGet(MdbLdap.TransTable, Name);

        if (name) {
            return name;
        }
    }

    return Name;
}

static char *
SchemaTransFunc(const char *Name, const char *Opt)
{
    char *name;

    name = BongoHashtableGet(MdbLdap.TransTable, Name);

    if (name) {
        return strdup(name);
    }

    return strdup(Name);
}

/* Translate an attribute/data value structs to a hashtable of key/valarrays
   NOTE: Use with LdapCreate()
 */
static BongoHashtable *
MdbAttrsToLdapAttrs(MDBValueStruct *Attribute, MDBValueStruct *Data)
{
    char *type, *value, **values;
    MDBLDAPSchemaAttr *attr;
    int inc = 12, last;
    unsigned int i;
    BongoHashtable *attrs;

    attrs = BongoHashtableCreateFull(128, (HashFunction)BongoStringHash, (CompareFunction)strcasecmp, NULL, free);

    if (attrs) {
        for (i = 0; i < Attribute->Used; i++) {
            value = (char *)(Data->Value[i]);
            type = (char *)(Attribute->Value[i]);

            if (type[0] == 'S' || type[0] == 'D') {
                type = TranslateSchemaName(type + 1);
                attr = BongoHashtableGet(MdbLdap.AttrTable, type);
            }

            if (!attr) {
                type = TranslateSchemaName((char *)Attribute->Value[i]);
                attr = BongoHashtableGet(MdbLdap.AttrTable, type);
            }

            if (!attr) {
                break;
            }

            values = BongoHashtableGet(attrs, type);

            if (!values) {
                values = NewValArray(inc);

                if (!values) {
                    break;
                }

                if (BongoHashtablePut(attrs, type, values)) {
                    break;
                }
            }

            last = CountValArray(values);

            if (!(last % inc) && last) {
                if (!ExpandValArray(&values, last + inc)) {
                    break;
                }
            }

            if (MDBLDAP_SYNTAX_DN(attr->syntax)) {
                value = SafeMdbToLdap(value, (char *)Attribute->base);

                if (!value) {
                    break;
                }
            }

            values[last] = value;
        }

        if (i != Attribute->Used) {
            BongoHashtableDelete(attrs);
            attrs = NULL;
        }
    }

    return attrs;
}

/* end internal functions */
/* begin external driver interface functions */

static BOOL
MDBLDAPGetServerInfo(unsigned char *ServerDN, unsigned char *ServerTree,
                     MDBValueStruct *V)
{
    if (ServerDN) {
        strcpy((char *)ServerDN, MdbLdap.ServerDn);
    }

    if (ServerTree) {
        /* I'm not positive I can get away with this. But since the 'tree' in
           MDB and eDir is very similar to what we're calling the Base DN I'm
           hoping it won't cause any serious problems inside Bongo. */
        strcpy((char *)ServerTree, MdbLdap.MdbBaseDn);
    }

    return TRUE;
}

static char *
MDBLDAPGetBaseDN(MDBValueStruct *V)
{
    return(MdbLdap.MdbBaseDn);
}

static MDBHandle
MDBLDAPAuthenticate(const unsigned char *Object,
                    const unsigned char *Password,
                    const unsigned char *Arguments)
{
    MDBLDAPContextStruct *context = NULL;

    context = CreateContext((char *)Object, (char *)Password);

    /* no handle lists right now..
    if (context) {
        if (!HandleListAdd((MDBHandle) context)) {
             if (context->binddn && context->passwd) {
                 free(context->binddn);
                 free(context->passwd);
             }

             ldap_unbind_s(context->ldap);
             DestroyContext(context);
             context = NULL;
        }
    }
    */

    return (MDBHandle) context;
}

static BOOL
MDBLDAPRelease(MDBHandle Handle)
{
    if (Handle) {
        DestroyContext(Handle);
        return TRUE;
    }

    return FALSE;
}

static MDBValueStruct *
MDBLDAPCreateValueStruct(MDBHandle Handle, const unsigned char *Context)
{
    MDBValueStruct *v;

    v = malloc(sizeof(MDBValueStruct));

    if (v) {
        memset(v, 0, sizeof(MDBValueStruct));
        v->duplicate = FALSE;
        v->context = (MDBLDAPContextStruct *) Handle;

        if (Context) {
            v->base = (unsigned char *)GetAbsoluteDn((const char *)Context, NULL);

            if (!v->base) {
                free(v);
                return NULL;
            }
        } else {
            v->base = NULL;
        }

        /* Get connections as needed using INIT_VS_LDAP macro
        v->ldap = GetLdapConnection(Handle);

        if (!v->ldap) {
            if (v->base) {
                free(v->base);
            }

            free(v);
            v = NULL;
        }
        */
    }

    return v;
}

static BOOL
MDBLDAPDestroyValueStruct(MDBValueStruct *V)
{
    BOOL result = TRUE;

    if (V->base && !V->duplicate) {
        free(V->base);
    }

    if (V->ldap) {
        result = ReleaseLdapConnection(V->context, V->ldap);
    }

    MDBLDAPFreeValues(V);
    free(V);

    return result;
}

static MDBValueStruct *
MDBLDAPShareContext(MDBValueStruct *V)
{
    MDBValueStruct *v;

    v = malloc(sizeof(MDBValueStruct));

    if (v) {
        memset(v, 0, sizeof(MDBValueStruct));
        v->duplicate = TRUE;
        v->context = V->context;
        v->base = V->base;

        /* Get connection as needed using INIT_VS_LDAP macro
        v->ldap = GetLdapConnection(V->context);

        if (!v->ldap) {
            free(v);
            v = NULL;
        }
        */
    }

    return v;
}

static BOOL
MDBLDAPSetValueStructContext(const unsigned char *Context, MDBValueStruct *V)
{
    char *mdbDn = NULL;

    if (Context) {
        mdbDn = GetAbsoluteDn((char *)Context, (char *)V->base);
    } else {
        mdbDn = strdup(MdbLdap.MdbBaseDn);
    }

    if (mdbDn) {
        if (V->base && !V->duplicate) {
            free(V->base);
        }

        if (V->duplicate) {
            V->duplicate = FALSE;
        }

        V->base = (unsigned char *)mdbDn;
        return TRUE;
    }

    return FALSE;
}

static MDBEnumStruct *
MDBLDAPCreateEnumStruct(MDBValueStruct *V)
{
    MDBEnumStruct *e = NULL;

    e = malloc(sizeof(MDBEnumStruct));

    if (e) {
        e->vals = NULL;
        e->next = 0;
        e->count = 0;
    }

    return e;
}

static BOOL
MDBLDAPDestroyEnumStruct(MDBEnumStruct *E, MDBValueStruct *V)
{
    if (E) {
        if (E->vals) {
            FreeValArray(E->vals);
            free(E->vals);
        }

        free(E);
        return TRUE;
    }

    return FALSE;
}

BOOL
MDBLDAPFreeValues(MDBValueStruct * V)
{
    unsigned int i;

    if (V->allocated) {
        for (i = 0; i < V->Used; i++) {
            free(V->Value[i]);
        }

        free(V->Value);
        V->allocated = 0;
        V->Value = NULL;
        V->Used = 0;
    }

    return TRUE;
}

BOOL
MDBLDAPAddValue(const unsigned char *Value, MDBValueStruct *V)
{
    char *ptr;

    if (V->Used + 1 > V->allocated) {
        ptr = realloc(V->Value, (V->allocated + VALUE_ALLOC_SIZE) *
                      sizeof(unsigned char *));

        if (ptr) {
            V->Value = (unsigned char **) ptr;
            V->allocated += VALUE_ALLOC_SIZE;
        } else {
            if (V->allocated) {
                MDBLDAPFreeValues(V);

                if (V->Value) {
                    free(V->Value);
                }
            }

            V->Value = NULL;
            V->Used = 0;
            V->allocated = 0;
            return FALSE;
        }
    }

    V->Value[V->Used] = (unsigned char *)strdup((char *)Value);

    if (V->Value[V->Used]) {
        V->Used++;
        return TRUE;
    }

    return FALSE;
}

static BOOL
MDBLDAPFreeValue(unsigned long Index, MDBValueStruct * V)
{
    if (Index < V->Used) {
        free(V->Value[Index]);

        if (Index < (V->Used - 1)) {
            memmove(&V->Value[Index], &V->Value[Index + 1],
                    ((V->Used - 1) - Index) * sizeof(unsigned char *));
        }

        V->Used--;

        return TRUE;
    }

    return FALSE;
}

static long
MDBLDAPRead(const unsigned char *Object, const unsigned char *Attribute,
            MDBValueStruct *V)
{
    char *ldapDn;
    char **vals;
    char *attribute;
    long result = 0;
    MDBLDAPSchemaAttr *attr;

    attribute = TranslateSchemaName((char *)Attribute);
    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        attr = BongoHashtableGet(MdbLdap.AttrTable, attribute);

        if (attr) {
            INIT_VS_LDAP(V);
            vals = LdapRead(ldapDn, attribute, V->ldap);

            if (vals) {
                if (attr->syntax && MDBLDAP_SYNTAX_DN(attr->syntax)) {
                    result = TranslateValArrayToValueStruct(vals, V, 
                                                            LdapToRelativeMdb, 
                                                            (char *)V->base);
                } else {
                    result = ValArrayToValueStruct(vals, V);
                }

                FreeValArray(vals);
                free(vals);
            }
        }
        
        free(ldapDn);
    }

    return result;
}

/**
 * Since ldap_get_values() is the only way to retrieve attribute values, we 
 * have to do it the same as MDBLDAPRead().
 */
static const unsigned char *
MDBLDAPReadEx(const unsigned char *Object, const unsigned char *Attribute,
              MDBEnumStruct * E, MDBValueStruct *V)
{
    char *ldapDn;
    char **vals;
    char *attribute;
    MDBLDAPSchemaAttr *attr;

    if (!E->vals) {
        attribute = TranslateSchemaName((char *)Attribute);
        ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

        if (ldapDn) {
            attr = BongoHashtableGet(MdbLdap.AttrTable, attribute);

            if (attr) {
                INIT_VS_LDAP(V);
                vals = LdapRead(ldapDn, attribute, V->ldap);

                if (vals) {
                    if (attr->syntax && MDBLDAP_SYNTAX_DN(attr->syntax)) {
                        E->vals = TranslateValArray(vals, LdapToRelativeMdb, 
                                                    (char *)V->base);
                        FreeValArray(vals);
                        free(vals);
                    } else {
                        E->vals = vals;
                    }

                    if (E->vals) {
                        E->count = CountValArray(E->vals);
                        E->next = 0;
                    }
                }
            }

            free(ldapDn);
        }
    }

    if (E->vals && E->next < E->count) {
        E->next++;
        return (unsigned char *)E->vals[E->next - 1];
    }

    /* should this be handled only during *Destroy? */
    if (E->vals) {
        FreeValArray(E->vals);
        free(E->vals);
        E->vals = NULL;
    }

    return NULL;
}

static long
MDBLDAPReadDN(const unsigned char *Object, const unsigned char *Attribute,
              MDBValueStruct *V)
{
    char *ldapDn;
    char **vals;
    char *attribute;
    long result = 0;
    MDBLDAPSchemaAttr *attr;

    attribute = TranslateSchemaName((char *)Attribute);
    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        attr = BongoHashtableGet(MdbLdap.AttrTable, attribute);

        if (attr) {
            INIT_VS_LDAP(V);
            vals = LdapRead(ldapDn, attribute, V->ldap);

            if (vals) {
                if (attr->syntax && MDBLDAP_SYNTAX_DN(attr->syntax)) {
                    result = TranslateValArrayToValueStruct(vals, V, 
                                                            LdapToRelativeMdb, 
                                                            (char *)V->base);
                } else {
                    result = ValArrayToValueStruct(vals, V);
                }

                FreeValArray(vals);
                free(vals);
            }
        }
        
        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPWrite(const unsigned char *Object, const unsigned char *Attribute, 
             MDBValueStruct *V)
{
    char **vals;
    char *ldapDn;
    char *attribute;
    unsigned int count;
    BOOL result = FALSE;

    attribute = TranslateSchemaName((char *)Attribute);
    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        if (V->Used) {
            vals = NewValArray(V->Used);

            if (vals) {
                count = ValueStructToValArray(V, vals);

                if (count) {
                    result = TRUE;
                }
            }
        } else {
            vals = NULL;
            result = TRUE;
        }

        if (result) {
            INIT_VS_LDAP(V);
            result = LdapWrite(ldapDn, attribute, vals, LDAP_MOD_REPLACE, 
                               V->ldap);
        }

        if (vals) {
            FreeValArray(vals);
            free(vals);
        }

        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPWriteDN(const unsigned char *Object, const unsigned char *Attribute,
               MDBValueStruct *V)
{
    char **vals;
    char *ldapDn;
    char *attribute;
    unsigned int count;
    BOOL result = FALSE;

    attribute = TranslateSchemaName((char *)Attribute);
    /* should we check to see if Attribute is a DN type? */
    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        vals = NewValArray(V->Used);

        if (vals) {
            /* the docs say this value can be relative or absolute but since I
               can't translate a relative MDB DN into an LDAP DN, I'm going to
               force them all to be absolute. */
            count = TranslateValueStructToValArray(V, vals, SafeMdbToLdap, 
                                                   (char *)V->base);

            if (count) {
                INIT_VS_LDAP(V);
                result = LdapWrite(ldapDn, attribute, vals, LDAP_MOD_REPLACE, 
                                   V->ldap);
            }

            FreeValArray(vals);
            free(vals);
        }

        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPClear(const unsigned char *Object, const unsigned char *Attribute, 
             MDBValueStruct *V)
{
    char *ldapDn;
    char *attribute;
    BOOL result = FALSE;

    attribute = TranslateSchemaName((char *)Attribute);
    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        INIT_VS_LDAP(V);
        result = LdapWrite(ldapDn, attribute, NULL, LDAP_MOD_DELETE, 
                           V->ldap);
        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPAdd(const unsigned char *Object, const unsigned char *Attribute, 
           const unsigned char *Value, MDBValueStruct *V)
{
    char *vals[2] = {(char *)Value, NULL};
    char *ldapDn;
    char *attribute;
    BOOL result = FALSE;

    attribute = TranslateSchemaName((char *)Attribute);
    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        INIT_VS_LDAP(V);
        result = LdapWrite(ldapDn, attribute, vals, LDAP_MOD_ADD, 
                           V->ldap);
        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPAddDN(const unsigned char *Object, const unsigned char *Attribute, 
             const unsigned char *Value, MDBValueStruct *V)
{
    char *vals[2] = {NULL, NULL};
    char *ldapDn;
    char *attribute;
    BOOL result = FALSE;

    attribute = TranslateSchemaName((char *)Attribute);
    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        vals[0] = SafeMdbToLdap((char *)Value, NULL);

        if (vals[0]) {
            INIT_VS_LDAP(V);
            result = LdapWrite(ldapDn, attribute, vals, LDAP_MOD_ADD, 
                               V->ldap);
            free(vals[0]);
        }

        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPRemove(const unsigned char *Object, const unsigned char *Attribute,
              const unsigned char *Value, MDBValueStruct *V)
{
    char *vals[2] = {(char *)Value, NULL};
    char *ldapDn;
    char *attribute;
    BOOL result = FALSE;

    attribute = TranslateSchemaName((char *)Attribute);
    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        INIT_VS_LDAP(V);
        result = LdapWrite(ldapDn, attribute, vals, LDAP_MOD_DELETE, 
                           V->ldap);
        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPRemoveDN(const unsigned char *Object, const unsigned char *Attribute, 
                const unsigned char *Value, MDBValueStruct *V)
{
    char *vals[2] = {NULL, NULL};
    char *ldapDn;
    char *attribute;
    BOOL result = FALSE;

    attribute = TranslateSchemaName((char *)Attribute);
    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        vals[0] = SafeMdbToLdap((char *)Value, NULL);

        if (vals[0]) {
            INIT_VS_LDAP(V);
            result = LdapWrite(ldapDn, attribute, vals, LDAP_MOD_DELETE, 
                               V->ldap);
            free(vals[0]);
        }

        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPIsObject(const unsigned char *Object, MDBValueStruct *V)
{
    char *ldapDn;
    LDAPMessage *res;
    char *attrs[2] = {"dn", NULL};
    int err;
    BOOL result = FALSE;

    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        INIT_VS_LDAP(V);

        if (V->ldap) {
            err = ldap_search_s(V->ldap, ldapDn, LDAP_SCOPE_BASE, 
                                "(objectClass=*)", attrs, 0, &res);

            if (err == LDAP_SUCCESS) {
                ldap_msgfree(res);
                result = TRUE;
            } else {
                Log(LOG_ERROR, "ldap_search_s() failed: %s", 
                    ldap_err2string(err));
                Log(LOG_ERROR, "dn: %s", ldapDn);
            }
        }

        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPGetObjectDetailsEx(const unsigned char *Object, MDBValueStruct *Types,
                          unsigned char *RDN, unsigned char *DN,
                          MDBValueStruct *V)
{
    BOOL result = FALSE;
    char *absDn;
    char *ldapDn;
    char **classes;
    char *absDnReal;
    char *ldapDnReal;
    char **classesReal;
    char **vals;

    absDn = GetAbsoluteDn((char *)Object, (char *)V->base);
    if (absDn) {
        ldapDn = MdbToLdap(absDn);
        if (ldapDn) { 
            INIT_VS_LDAP(V);	
            classes = LdapRead(ldapDn, "objectClass", V->ldap);
            if (classes) {
                char **objectClass = &classes[0];
                if (*objectClass) {
                    result = TRUE;
                    do {
                        if (strcmp(*objectClass, "alias")) {
                            objectClass++;
                            continue;
                        }

                        /* we have an alias, we need to find the real object */
                        vals = LdapRead(ldapDn, "aliasedObjectName", V->ldap);
                        if (vals) {
                            if (vals[0][0]) {
                                ldapDnReal = strdup(vals[0]);
                                if (ldapDnReal) {
                                    absDnReal = LdapToMdb(ldapDnReal);
                                    if(absDnReal) {
                                        classesReal = LdapRead(ldapDnReal, "objectClass", V->ldap);
                                        if (classesReal) {
                                            /* the aliased object is in good shape; switch objects */
                                            FreeValArray(classes);
                                            free(classes);
                                            classes = classesReal;
                                            free(absDn);
                                            absDn = absDnReal;
                                            free(ldapDn);
                                            ldapDn = ldapDnReal;
                                            break;
                                        }
                                        free(absDnReal);
                                    }
                                    free(ldapDnReal);
                                }
                            }
                            FreeValArray(vals);
                            free(vals);
                        }
                        result = FALSE;
                        break;
                    } while(*objectClass != NULL);
 
                    if (result) {
                        /* we have everything we need (ldapDn, absDn, and classes) to fulfill the request. */

                        if (DN) {
                            strcpy((char *)DN, absDn);
                        }

                        if (Types) {
                            TranslateValArrayToValueStruct(classes, Types, 
                                                           SchemaTransFunc, 
                                                           NULL);
                        }

                        if (RDN) {
                            char *relDn = GetRelativeDn((char *)Object, (char *)V->base);

                            if (relDn) {
                                strcpy((char *)RDN, relDn);
                                free(relDn);
                            } else {
                                result = FALSE;
                            }
                        }
                    }
                }

                FreeValArray(classes);
                free(classes);
            }
            free(ldapDn);
        }
        free(absDn);
    }
    return(result);
}

static BOOL 
MDBLDAPGetObjectDetails(const unsigned char *Object, unsigned char *Type, 
                        unsigned char *RDN, unsigned char *DN, 
                        MDBValueStruct *V)
{
    BOOL result = FALSE;
    MDBValueStruct *types = MDBLDAPShareContext(V);

    if (types) {
        result = MDBLDAPGetObjectDetailsEx(Object, types, RDN, DN, V);

        if (result && Type && types->Used > 0) {
            strcpy((char *) Type, (char *) types->Value[0]);
        }

        MDBLDAPDestroyValueStruct(types);
    }

    return result;
}

static BOOL
MDBLDAPVerifyPassword(const unsigned char *Object, 
                      const unsigned char *Password, MDBValueStruct *V)
{
    char *ldapDn;
    BOOL result = FALSE;

    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        result = LdapVerify(ldapDn, (char *)Password);
        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPChangePassword(const unsigned char *Object, 
                      const unsigned char *OldPassword, 
                      const unsigned char *NewPassword, MDBValueStruct *V)
{
    char *ldapDn;
    BOOL result = FALSE;

    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        INIT_VS_LDAP(V);
        result = LdapPassword(ldapDn, (char *)OldPassword, (char *)NewPassword, V->ldap);
        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPChangePasswordEx(const unsigned char *Object,
                        const unsigned char *OldPassword,
                        const unsigned char *NewPassword, MDBValueStruct *V)
{
    char *ldapDn;
    BOOL result = FALSE;
    char *oldPassword = "";

    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        if (OldPassword) {
            oldPassword = (char *)OldPassword;
        }

        INIT_VS_LDAP(V);
        result = LdapPassword(ldapDn, oldPassword, (char *)NewPassword, V->ldap);
        free(ldapDn);
    }

    return result;
}

static long
MDBLDAPEnumerateObjects(const unsigned char *Container, 
                        const unsigned char *Type, 
                        const unsigned char *Pattern, MDBValueStruct *V)
{
    char *ldapDn;
    char **vals;
    char *filter;
    char *type;
    long result = 0;

    type = TranslateSchemaName((char *)Type);
    ldapDn = SafeMdbToLdap((char *)Container, (char *)V->base);

    if (ldapDn) {
        filter = LdapFilter(type, (char *)Pattern);

        if (filter) {
            INIT_VS_LDAP(V);
            vals = LdapSearch(ldapDn, filter, LDAP_SCOPE_ONELEVEL, -1, V->ldap);

            if (vals) {
                result = TranslateValArrayToValueStruct(vals, V, 
                                                        LdapToRelativeMdb,
                                                        (char *)V->base);
                FreeValArray(vals);
                free(vals);
            }

            free(filter);
        }

        free(ldapDn);
    }

    return result;
}

static const unsigned char *
MDBLDAPEnumerateObjectsEx(const unsigned char *Container, 
                          const unsigned char *Type,
                          const unsigned char *Pattern, unsigned long Flags,
                          MDBEnumStruct *E, MDBValueStruct *V)
{
    char *ldapDn;
    char **vals;
    char *filter;
    char *type;

    if (!E->vals) {
        type = TranslateSchemaName((char *)Type);
        ldapDn = SafeMdbToLdap((char *)Container, (char *)V->base);

        if (ldapDn) {
            filter = LdapFilter(type, (char *)Pattern);

            if (filter) {
                INIT_VS_LDAP(V);
                vals = LdapSearch(ldapDn, filter, LDAP_SCOPE_ONELEVEL, -1, V->ldap);

                if (vals) {
                    E->vals = TranslateValArray(vals, LdapToAbsoluteMdb, 
                                                (char *)V->base);
                    if (E->vals) {
                        E->count = CountValArray(E->vals);
                        E->next = 0;
                    }

                    free(vals);
                }

                free(filter);
            }

            free(ldapDn);
        }
    }

    if (E->vals && E->next < E->count) {
        E->next++;
        return (unsigned char *)E->vals[E->next - 1];
    }

    /* should this be handled only during *Destroy? */
    if (E->vals) {
        FreeValArray(E->vals);
        free(E->vals);
        E->vals = NULL;
    }

    return NULL;
}

static const unsigned char *
MDBLDAPEnumerateAttributesEx(const unsigned char *Object, MDBEnumStruct *E, 
                             MDBValueStruct *V)
{
    char *ldapDn;
    char **vals;

    if (!E->vals) {
        ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

        if (ldapDn) {
            INIT_VS_LDAP(V);
            vals = LdapAttributes(ldapDn, V->ldap);

            if (vals) {
                E->vals = TranslateValArray(vals, SchemaTransFunc, NULL);

                if (E->vals) {
                    E->count = CountValArray(E->vals);
                    E->next = 0;
                }

                free(vals);
            }

            free(ldapDn);
        }
    }

    if (E->vals && E->next < E->count) {
        E->next++;
        return (unsigned char *)E->vals[E->next - 1];
    }

    /* should this be handled only during *Destroy? */
    if (E->vals) {
        FreeValArray(E->vals);
        free(E->vals);
        E->vals = NULL;
    }

    return NULL;
}

static BOOL
MDBLDAPCreateAlias(const unsigned char *Alias, 
                   const unsigned char *RealObject, MDBValueStruct *V)
{
    BOOL result = FALSE;
    BongoHashtable *attrs;
    char *classes[3];
    char *values[2];
    char *ldapRealDn;
    char *ldapAliasDn;
    char *ptr;

    /* get the ldap dn for the real object */
    ldapRealDn = SafeMdbToLdap((char *)RealObject, (char *)V->base);
    if (ldapRealDn) {
        /* generate the ldap dn for the alias object*/
        /* we can get the type from the real object dn */
        
        ptr = strchr(ldapRealDn, '=');
        if (ptr) {
            *ptr = '\0';
            ldapAliasDn = NewSafeMdbToLdap((char *)Alias, ldapRealDn, (char *)V->base);
            *ptr = '=';
            if (ldapAliasDn) {
                attrs = BongoHashtableCreate(128,(HashFunction)BongoStringHash, (CompareFunction)strcasecmp);
                if (attrs) {
                    values[0] = ldapRealDn;
                    values[1] = NULL;
                    if (!BongoHashtablePut(attrs, "aliasedObjectName", values)) {
                        /* set up classes */
                        classes[0] = "alias";
                        classes[1] = "extensibleObject";
                        classes[2] = NULL;

                        INIT_VS_LDAP(V);
                        result = LdapCreate(ldapAliasDn, classes, attrs, V->ldap);
                    }
                    BongoHashtableDelete(attrs);
                }
                free(ldapAliasDn);
            }
        }
        free(ldapRealDn);
    }
    return(result);
}

static BOOL
MDBLDAPCreateObject(const unsigned char *Object, const unsigned char *Class,
                    MDBValueStruct *Attribute, MDBValueStruct *Data,
                    MDBValueStruct *V)
{
    MDBLDAPSchemaClass *class;
    char *classes[2];
    char *ldapDn = NULL;
    BongoHashtable *attrs;
    BOOL result = FALSE;

    classes[0] = TranslateSchemaName((char *)Class);
    classes[1] = NULL;

    class = BongoHashtableGet(MdbLdap.ClassTable, classes[0]);

    /* make a new ldap dn for the object */
    if (class) {
        ldapDn = NewSafeMdbToLdap((char *)Object, class->naming, (char *)V->base);
    }

    if (ldapDn) {
        attrs = MdbAttrsToLdapAttrs(Attribute, Data);

        if (attrs) {
            INIT_VS_LDAP(V);
            result = LdapCreate(ldapDn, classes, attrs, V->ldap);
            BongoHashtableDelete(attrs);
        }

        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPDeleteObject(const unsigned char *Object, BOOL Recursive, 
                    MDBValueStruct *V)
{
    char *ldapDn;
    char **vals;
    char *filter;
    int i;
    int err = 0;
    BOOL result = FALSE;

    ldapDn = SafeMdbToLdap((char *)Object, (char *)V->base);

    if (ldapDn) {
        if (Recursive) {
            filter = LdapFilter(NULL, NULL);

            if (filter) {
                err = 0;
                INIT_VS_LDAP(V);
                vals = LdapSearch(ldapDn, filter, LDAP_SCOPE_ONELEVEL, -1, V->ldap);

                if (vals) {
                    for (i = 0; !err && vals[i]; i++) {
                        if (!MDBLDAPDeleteObject((unsigned char *)vals[i], TRUE, V)) {
                            err = 1;
                        }
                    }

                    free(vals);
                }

                free(filter);
            } else {
                err = 1;
            }
        }

        if (!err) {
            INIT_VS_LDAP(V);

            if (V->ldap) {
                err = ldap_delete_s(V->ldap, ldapDn);

                if (err == LDAP_SUCCESS) {
                    result = TRUE;
                } else {
                    Log(LOG_ERROR, "ldap_delete_s() failed: %s", 
                        ldap_err2string(err));
                    Log(LOG_ERROR, "dn: %s", ldapDn);
                }
            }
        }

        free(ldapDn);
    }

    return result;
}

static BOOL
MDBLDAPRenameObject(const unsigned char *ObjectOld, 
                    const unsigned char *ObjectNew, MDBValueStruct *V)
{
    char *baseOld;
    char *baseNew;
    char *nameOld;
    char *nameNew;
    char *newSuperior = NULL;
    char *ldapDn;
    char *naming;
    char *newRdn;
    char *ptr;
    int err = 0;
    BOOL result = FALSE;

    baseOld = strdup((char *)ObjectOld);

    if (baseOld) {
        nameOld = strrchr(baseOld, '\\');
        *nameOld++ = '\0';

        baseNew = strdup((char *)ObjectNew);

        if (baseNew) {
            nameNew = strrchr(baseNew, '\\');
            *nameNew++ = '\0';

            if (strcmp(baseOld, baseNew)) {
                newSuperior = SafeMdbToLdap(baseNew, (char *)V->base);

                if (!newSuperior) {
                    err = 1;
                }
            }

            if (!err) {
                ldapDn = SafeMdbToLdap((char *)ObjectOld, (char *)V->base);

                if (ldapDn) {
                    naming = strdup(ldapDn);

                    if (naming) {
                        ptr = strchr(naming, '=');
                        *ptr++ = '\0';

                        newRdn = malloc(strlen(naming) + strlen(nameNew) + 2);

                        if (newRdn) {
                            INIT_VS_LDAP(V);

                            if (V->ldap) {
                                sprintf(newRdn, "%s=%s", naming, nameNew);
                                err = ldap_rename_s(V->ldap, ldapDn, newRdn, 
                                                newSuperior, TRUE, NULL, NULL);

                                if (err == LDAP_SUCCESS) {
                                    result = TRUE;
                                } else {
                                    Log(LOG_ERROR, 
                                        "ldap_rename_s() failed: %s",
                                        ldap_err2string(err));
                                    Log(LOG_ERROR, "dn: %s", ldapDn);

                                    if (newSuperior) {
                                        Log(LOG_ERROR, "new dn: %s,%s", newRdn, 
                                            newSuperior);
                                    } else {
                                        Log(LOG_ERROR, "new rdn: %s", newRdn);
                                    }
                                }
                            }

                            free(newRdn);
                        }

                        free(naming);
                    }

                    free(ldapDn);
                }
            }

            free(baseNew);
        }

        free(baseOld);
    }

    return result;
}

/* Most of the following functions cannot be easily implemented in LDAP */

static BOOL
MDBLDAPDefineAttribute(const unsigned char *Attribute, 
                       const unsigned char *ASN1, unsigned long Type, 
                       BOOL SingleValue, BOOL ImmediateSync, BOOL Public, MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Function MDBDefineAttribute() is not supported by MBDLDAP driver.");
    return FALSE;
}

static BOOL
MDBLDAPUndefineAttribute(const unsigned char *Attribute, MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Function MDBUndefineAttribute() is not supported by MBDLDAP driver.");
    return FALSE;
}

static BOOL
MDBLDAPDefineClass(const unsigned char *Class, const unsigned char *ASN1, 
                   BOOL Container, MDBValueStruct *Superclass, 
                   MDBValueStruct *Containment, MDBValueStruct *Naming, 
                   MDBValueStruct *Mandatory, MDBValueStruct *Optional, 
                   MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Function MDBDefineClass() is not supported by MBDLDAP driver.");
    return FALSE;
}

static BOOL
MDBLDAPAddAttribute(const unsigned char *Attribute, const unsigned char *Class,
                    MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Function MDBAddAttribute() is not supported by MBDLDAP driver.");
    return FALSE;
}

static BOOL
MDBLDAPUndefineClass(const unsigned char *Class, MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Function MDBUndefineClass() is not supported by MBDLDAP driver.");
    return FALSE;
}

static BOOL
MDBLDAPListContainableClasses(const unsigned char *Object, MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Function MDBListContainableClasses() is not supported by MBDLDAP driver.");
    return FALSE;
}

static const unsigned char *
MDBLDAPListContainableClassesEx(const unsigned char *Container,
                               MDBEnumStruct *E, MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Function MDBListContainableClassesEx() is not supported by MBDLDAP driver.");
    return NULL;
}

static BOOL
MDBLDAPGrantObjectRights(const unsigned char *Object, 
                         const unsigned char *TrusteeDN, BOOL Read, BOOL Write,
                         BOOL Delete, BOOL Rename, BOOL Admin, 
                         MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Function MDBGrantObjectRights() is not supported by MBDLDAP driver.");
    return FALSE;
}

static BOOL
MDBLDAPGrantAttributeRights(const unsigned char *Object, 
                            const unsigned char *Attribute, 
                            const unsigned char *TrusteeDN, BOOL Read, 
                            BOOL Write, BOOL Admin, MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Function MDBGrantAttributeRights() is not supported by MBDLDAP driver.");
    return FALSE;
}

/* Undocumented functions */

static BOOL
MDBLDAPAddAddressRestriction(const unsigned char *Object,
                             const unsigned char *Server, MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Undocumented function MDBAddAddressRestriction() requested.");
    return FALSE;
}

static BOOL
MDBLDAPWriteTyped(const unsigned char *Object, const unsigned char *Attribute, 
                  const int AttrType, MDBValueStruct *V)
{
    Log(LOG_ERROR,
        "Undocumented function MDBWriteTyped() requested.");
    return FALSE;
}

static BOOL
MDBLDAPFindObjects(const unsigned char *container,
                   MDBValueStruct *types,
                   const unsigned char *pattern,
                   MDBValueStruct *attributes,
                   int depth, 
                   int max,
                   MDBValueStruct *V)
{
    char *ldapDn;
    char **vals;
    char *filter;
    char *type;
    long result = FALSE;
    
    ldapDn = SafeMdbToLdap((char *)container, (char *)V->base);
    
    if (ldapDn) {
        filter = LdapSearchFilter(types, (char *)pattern, attributes);
        
        if (filter) {
            INIT_VS_LDAP(V);
            vals = LdapSearch(ldapDn, filter, depth, -1, V->ldap);
            
            if (vals) {
                result = TranslateValArrayToValueStruct(vals, V, 
                                                        LdapToAbsoluteMdb, 
                                                        (char *)V->base);
                FreeValArray(vals);
                free(vals);
            }
            
            free(filter);
        }
        
        free(ldapDn);
    }
    
    return result;
}

#if 0
static void
PathPrint(void *key, void *value, void *data)
{
    printf("%s: %s\n", key, value);
}
#endif

EXPORT BOOL
MDBLDAPInit(MDBDriverInitStruct *Init)
{
    int version = LDAP_VERSION3;
    char *mdbDn;
    char *ldapDn;
    BOOL result = TRUE;
    char *param;
    char *ptr;
    char *arg;
    char **vals;

    LogStartup();

    MdbLdap.args = NULL;
    MdbLdap.ServerTree[0] = '\0';
    MdbLdap.ServerDn[0] = '\0';
    MdbLdap.MdbBaseDn = NULL;
    MdbLdap.LdapBaseDn = NULL;
    MdbLdap.LdapSchemaDn = NULL;
    MdbLdap.LdapBindDn = NULL;
    MdbLdap.LdapBindPasswd = NULL;
    MdbLdap.LdapHostAddress = "localhost";
    MdbLdap.LdapHostPort = 389;
    MdbLdap.LdapTls = 0;
    MdbLdap.LdapMinConnections = MDBLDAP_MIN_CONNECTIONS;
    MdbLdap.LdapMaxConnections = MDBLDAP_MAX_CONNECTIONS;
    MdbLdap.Ldap = NULL;
    MdbLdap.PathTable = NULL;
    MdbLdap.AttrTable = NULL;
    MdbLdap.ClassTable = NULL;
    MdbLdap.TransTable = NULL;

    /* Parse configuration arguments */
    if (Init->Arguments) {
        MdbLdap.args = strdup((char *)(Init->Arguments));

        if (MdbLdap.args) {
            param = strtok_r(MdbLdap.args, "=", &ptr);

            while (param) {
                arg = strtok_r(NULL, "\"", &ptr);

                if (!strcasecmp(param, "binddn")) {
                    MdbLdap.LdapBindDn = arg;
                } else if (!strcasecmp(param, "password")) {
                    MdbLdap.LdapBindPasswd = arg;
                } else if (!strcasecmp(param, "host")) {
                    MdbLdap.LdapHostAddress = arg;
                } else if (!strcasecmp(param, "port")) {
                    MdbLdap.LdapHostPort = atoi(arg);
                } else if (!strcasecmp(param, "basedn")) {
                    MdbLdap.LdapBaseDn = arg;
                } else if (!strcasecmp(param, "poolmin")) {
                    MdbLdap.LdapMinConnections = atoi(arg);
                } else if (!strcasecmp(param, "poolmax")) {
                    MdbLdap.LdapMaxConnections = atoi(arg);
                } else if (!strcasecmp(param, "tls")) {
                    MdbLdap.LdapTls = atoi(arg);
                }

                param = strtok_r(NULL, "=,", &ptr);
            }
        } else {
            result = FALSE;
        }
    }

    if (result) {
        if (MdbLdap.LdapBaseDn) {
            /* Set global LDAP options */
            ldap_set_option(NULL, LDAP_OPT_PROTOCOL_VERSION, &version);
            MdbLdap.Ldap = InitLdapConnection(MdbLdap.LdapBindDn, 
                                              MdbLdap.LdapBindPasswd, 
                                              DriverRebindProc, NULL);
            if (MdbLdap.Ldap) {
                XplMutexInit(MdbLdap.LdapMutex);
            } else {
                sprintf((char *)(Init->Error), "Could not initialize LDAP connection");
                result = FALSE;
            }

            /*
            MdbLdap.Ldap = ldap_init(MdbLdap.LdapHostAddress, 
                                     MdbLdap.LdapHostPort);

            if (MdbLdap.Ldap) {
                err = ldap_start_tls_s(MdbLdap.Ldap, NULL, NULL);

                if (err == LDAP_SUCCESS) {
                    err = ldap_simple_bind_s(MdbLdap.Ldap, MdbLdap.LdapBindDn,
                                             MdbLdap.LdapBindPasswd);

                    if (err == LDAP_SUCCESS) {
                        ldap_set_rebind_proc(MdbLdap.Ldap, DriverRebindProc, NULL);
                        XplMutexInit(MdbLdap.LdapMutex);
                    } else {
                        Log(LOG_ERROR, "ldap_simple_bind_s() failed: %s",
                            ldap_err2string(err));
                        Log(LOG_ERROR, "dn: %s", MdbLdap.LdapBindDn);
                        result = FALSE;
                        MdbLdap.Ldap = NULL;
                    }
                } else {
                    Log(LOG_ERROR, "ldap_start_tls_s() failed: %s",
                        ldap_err2string(err));
                    result = FALSE;
                    MdbLdap.Ldap = NULL;
                }
            } else {
                Log(LOG_ERROR, "ldap_init() failed: %s", 
                    ldap_err2string(errno));
                Log(LOG_ERROR, "host: %s:%d", MdbLdap.LdapHostAddress, 
                    MdbLdap.LdapHostPort);
                result = FALSE;
            }
            */
        } else {
            char *err = "Parameter 'baseDn' required for MDB LDAP Init";
            sprintf((char *)(Init->Error), err);
            Log(LOG_ERROR, err);
            result = FALSE;
        }
    }

    if (result) {
        MdbLdap.MdbBaseDn = LdapToMdb(MdbLdap.LdapBaseDn);

        if (!MdbLdap.MdbBaseDn) {
            result = FALSE;
        }
    }

    /* Initialize MDB -> LDAP DN cache */
    if (result) {
        MdbLdap.PathTable = BongoHashtableCreateFull(MDBLDAP_DEFAULT_CACHE_SIZE,
                                                    (HashFunction)BongoStringHash, (CompareFunction)strcasecmp,
                                                    free, free);

        if (MdbLdap.PathTable) {
            XplRWLockInit(&(MdbLdap.PathLock));

            /* initialize the cache with the base DN */
            mdbDn = strdup(MdbLdap.MdbBaseDn);

            if (mdbDn) {
                ldapDn = strdup(MdbLdap.LdapBaseDn);

                if (ldapDn) {
                    XplRWWriteLockAcquire(&(MdbLdap.PathLock));

                    if (BongoHashtablePut(MdbLdap.PathTable, mdbDn, ldapDn)) {
                        result = FALSE;
                    }

                    XplRWWriteLockRelease(&(MdbLdap.PathLock));
                } else {
                    result = FALSE;
                }
            } else {
                result = FALSE;
            }
        } else {
            result = FALSE;
        }
    }

    /* Initialize supported LDAP extensions cache */
    if (result) {
        MdbLdap.ExtTable = BongoHashtableCreateFull(MDBLDAP_DEFAULT_CACHE_SIZE,
                                                   (HashFunction)BongoStringHash, (CompareFunction)strcasecmp,
                                                   free, NULL);

        if (MdbLdap.ExtTable) {
            if (!LoadExtensions()) {
                result = FALSE;
            }
        } else {
            result = FALSE;
        }
    }

    if (result) {
        XplMutexLock(MdbLdap.LdapMutex);
        vals = LdapRead("", "subschemaSubentry", MdbLdap.Ldap);
        XplMutexUnlock(MdbLdap.LdapMutex);

        if (vals && vals[0]) {
            MdbLdap.LdapSchemaDn = strdup(vals[0]);

            if (!MdbLdap.LdapSchemaDn) {
                result = FALSE;
            }

            FreeValArray(vals);
            free(vals);
        } else {
            Log(LOG_ERROR, "Unable to read subschemaSubentry attribute");
            result = FALSE;
        }
    }

    /* Initialize attributes schema cache */
    if (result) {
        MdbLdap.AttrTable = BongoHashtableCreateFull(MDBLDAP_DEFAULT_CACHE_SIZE,
                                                    (HashFunction)BongoStringHash, (CompareFunction)strcasecmp,
                                                    NULL, AttrDel);

        if (MdbLdap.AttrTable) {
            if (!LoadSchemaAttrs()) {
                result = FALSE;
            }
        } else {
            result = FALSE;
        }
    }

    /* Initialize the class schema cache */
    if (result) {
        MdbLdap.ClassTable = BongoHashtableCreateFull(
                                                    MDBLDAP_DEFAULT_CACHE_SIZE,
                                                    (HashFunction)BongoStringHash, (CompareFunction)strcasecmp,
                                                    NULL, ClassDel);

        if (MdbLdap.ClassTable) {
            if (!LoadSchemaClasses()) {
                result = FALSE;
            }
        } else {
            result = FALSE;
        }
    }

    /* Initialize the MDB/LDAP schema name translation cache */
    if (result) {
        MdbLdap.TransTable = BongoHashtableCreateFull(
                                                    MDBLDAP_DEFAULT_CACHE_SIZE,
                                                    (HashFunction)BongoStringHash, (CompareFunction)strcasecmp,
                                                    free, free);

        if (MdbLdap.TransTable) {
            if (!InitTranslations()) {
                result = FALSE;
            }
        } else {
            result = FALSE;
        }
    }

    if (result) {
        Init->Interface.MDBAdd = MDBLDAPAdd;
        Init->Interface.MDBAddAddressRestriction = MDBLDAPAddAddressRestriction;
        Init->Interface.MDBAddAttribute = MDBLDAPAddAttribute;
        Init->Interface.MDBAddDN = MDBLDAPAddDN;
        Init->Interface.MDBAddValue = MDBLDAPAddValue;
        Init->Interface.MDBAuthenticate = MDBLDAPAuthenticate;
        Init->Interface.MDBChangePassword = MDBLDAPChangePassword;
        Init->Interface.MDBChangePasswordEx = MDBLDAPChangePasswordEx;
        Init->Interface.MDBClear = MDBLDAPClear;
        Init->Interface.MDBCreateAlias = MDBLDAPCreateAlias;
        Init->Interface.MDBCreateEnumStruct = MDBLDAPCreateEnumStruct;
        Init->Interface.MDBCreateObject = MDBLDAPCreateObject;
        Init->Interface.MDBCreateValueStruct = MDBLDAPCreateValueStruct;
        Init->Interface.MDBDefineAttribute = MDBLDAPDefineAttribute;
        Init->Interface.MDBDefineClass = MDBLDAPDefineClass;
        Init->Interface.MDBDeleteObject = MDBLDAPDeleteObject;
        Init->Interface.MDBDestroyEnumStruct = MDBLDAPDestroyEnumStruct;
        Init->Interface.MDBDestroyValueStruct = MDBLDAPDestroyValueStruct;
        Init->Interface.MDBEnumerateAttributesEx = MDBLDAPEnumerateAttributesEx;
        Init->Interface.MDBEnumerateObjects = MDBLDAPEnumerateObjects;
        Init->Interface.MDBEnumerateObjectsEx = MDBLDAPEnumerateObjectsEx;
        Init->Interface.MDBFreeValue = MDBLDAPFreeValue;
        Init->Interface.MDBFreeValues = MDBLDAPFreeValues;
        Init->Interface.MDBGetBaseDN = MDBLDAPGetBaseDN;
        Init->Interface.MDBGetObjectDetails = MDBLDAPGetObjectDetails;
        Init->Interface.MDBGetObjectDetailsEx = MDBLDAPGetObjectDetailsEx;
        Init->Interface.MDBGetServerInfo = MDBLDAPGetServerInfo;
        Init->Interface.MDBGrantAttributeRights = MDBLDAPGrantAttributeRights;
        Init->Interface.MDBGrantObjectRights = MDBLDAPGrantObjectRights;
        Init->Interface.MDBIsObject = MDBLDAPIsObject;
        Init->Interface.MDBListContainableClasses = MDBLDAPListContainableClasses;
        Init->Interface.MDBListContainableClassesEx = MDBLDAPListContainableClassesEx;
        Init->Interface.MDBRead = MDBLDAPRead;
        Init->Interface.MDBReadDN = MDBLDAPReadDN;
        Init->Interface.MDBReadEx = MDBLDAPReadEx;
        Init->Interface.MDBRelease = MDBLDAPRelease;
        Init->Interface.MDBRemove = MDBLDAPRemove;
        Init->Interface.MDBRemoveDN = MDBLDAPRemoveDN;
        Init->Interface.MDBRenameObject = MDBLDAPRenameObject;
        Init->Interface.MDBSetValueStructContext = MDBLDAPSetValueStructContext;
        Init->Interface.MDBShareContext = MDBLDAPShareContext;
        Init->Interface.MDBUndefineAttribute = MDBLDAPUndefineAttribute;
        Init->Interface.MDBUndefineClass = MDBLDAPUndefineClass;
        Init->Interface.MDBVerifyPassword = MDBLDAPVerifyPassword;
        Init->Interface.MDBVerifyPasswordEx = MDBLDAPVerifyPassword;
        Init->Interface.MDBWrite = MDBLDAPWrite;
        Init->Interface.MDBWriteDN = MDBLDAPWriteDN;
        Init->Interface.MDBWriteTyped = MDBLDAPWriteTyped;
        Init->Interface.MDBFindObjects = MDBLDAPFindObjects;
    } else {
        MDBLDAPShutdown();
    }

    return result;
}

EXPORT void
MDBLDAPShutdown(void)
{
    int err;

    if (MdbLdap.Ldap) {
        err = ldap_unbind(MdbLdap.Ldap);
        MdbLdap.Ldap = NULL;

        if (err != LDAP_SUCCESS) {
            Log(LOG_ERROR, "ldap_unbind() failed: %s", ldap_err2string(err));
        }

        XplMutexDestroy(MdbLdap.LdapMutex);
    }

    if (MdbLdap.TransTable) {
        BongoHashtableDelete(MdbLdap.TransTable);
        MdbLdap.TransTable = NULL;
    }

    if (MdbLdap.AttrTable) {
        BongoHashtableDelete(MdbLdap.AttrTable);
        MdbLdap.AttrTable = NULL;
    }

    if (MdbLdap.ClassTable) {
        BongoHashtableDelete(MdbLdap.ClassTable);
        MdbLdap.ClassTable = NULL;
    }

    if (MdbLdap.ExtTable) {
        BongoHashtableDelete(MdbLdap.ExtTable);
        MdbLdap.ExtTable = NULL;
    }

    if (MdbLdap.PathTable) {
        BongoHashtableDelete(MdbLdap.PathTable);
        MdbLdap.PathTable = NULL;
        XplRWLockDestroy(&(MdbLdap.PathLock));
    }

    if (MdbLdap.MdbBaseDn) {
        free(MdbLdap.MdbBaseDn);
        MdbLdap.MdbBaseDn = NULL;
    }

    if (MdbLdap.LdapSchemaDn) {
        free(MdbLdap.LdapSchemaDn);
        MdbLdap.LdapSchemaDn = NULL;
    }

    if (MdbLdap.args) {
        free(MdbLdap.args);
        MdbLdap.args = NULL;
    }

    LogShutdown();
}
