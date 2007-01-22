/* Product defines */
#define PRODUCT_NAME        "MDB ODBC Driver"
#define PRODUCT_VERSION     "$Revision:   1.1  $"

#include <config.h>
#include <xpl.h>
#include <bongoutil.h>
#include <gnutls/openssl.h>

#include <errno.h>
#include <ctype.h>

#include <sql.h>
#include <sqlext.h>
#include <sqltypes.h>

#include "mdbodbcp.h"
#include <mdb.h>

struct  {
    BOOL unload;

    struct {
        XplSemaphore shutdown;
    } sem;

    struct {
        XplThreadID main;
        XplThreadID group;
    } id;

    struct {
        unsigned char dsn[256 + 1];
        unsigned char user[256 + 1];
        unsigned char pass[256 + 1];
        unsigned char servername[64 + 1];
        unsigned char treename[64 + 1];
        unsigned long treenameLength;

        SQLINTEGER defaultContext;

        /* Special Attibutes, which require extra actions */
        struct {
            SQLINTEGER member;
        } special;
    } config;

    SQLHENV env;
} MDBodbc;

/* Prototypes for registration functions */

BOOL MDBodbcGetServerInfo(unsigned char *ServerDN, unsigned char *ServerTree, MDBValueStruct *V);
char *MDBodbcGetServerInfo(MDBValueStruct *V);
MDBHandle MDBodbcAuthenticate(const unsigned char *Object, const unsigned char *Password, const unsigned char *Arguments);
BOOL MDBodbcRelease(MDBHandle Context);
MDBValueStruct *MDBodbcCreateValueStruct(MDBHandle Handle, const unsigned char *Context);
BOOL MDBodbcFreeValues(MDBValueStruct *V);
BOOL MDBodbcDestroyValueStruct(MDBValueStruct *V);
MDBEnumStruct *MDBodbcCreateEnumStruct(MDBValueStruct *V);
BOOL MDBodbcDestroyEnumStruct(MDBEnumStruct *ES, MDBValueStruct *V);
BOOL MDBodbcSetValueStructContext(const unsigned char *Context, MDBValueStruct *V);
MDBValueStruct *MDBodbcShareContext(MDBValueStruct *V);
BOOL MDBodbcAddValue(const unsigned char *Value, MDBValueStruct *V);
BOOL MDBodbcFreeValue(unsigned long Index, MDBValueStruct *V);
BOOL MDBodbcDefineAttribute(const unsigned char *Attribute, const unsigned char *ASN1, unsigned long Type, BOOL SingleValue, BOOL ImmediateSync, BOOL Public, MDBValueStruct *V);
BOOL MDBodbcAddAttribute(const unsigned char *Attribute, const unsigned char *Class, MDBValueStruct *V);
BOOL MDBodbcUndefineAttribute(const unsigned char *Attribute, MDBValueStruct *V);
BOOL MDBodbcDefineClass(const unsigned char *Class, const unsigned char *ASN1, BOOL Container, MDBValueStruct *Superclass, MDBValueStruct *Containment, MDBValueStruct *Naming, MDBValueStruct *Mandatory, MDBValueStruct *Optional, MDBValueStruct *V);
BOOL MDBodbcUndefineClass(const unsigned char *Class, MDBValueStruct *V);
static __inline BOOL MDBodbcUndefineClassByID(unsigned long class, MDBValueStruct *V);
BOOL MDBodbcListContainableClasses(const unsigned char *Object, MDBValueStruct *V);
BOOL MDBodbcWriteTyped(const unsigned char *Object, const unsigned char *Attribute, const int AttrType, MDBValueStruct *V);
BOOL MDBodbcWrite(const unsigned char *Object, const unsigned char *AttributeIn, MDBValueStruct *V);
BOOL MDBodbcWriteDN(const unsigned char *Object, const unsigned char *AttributeIn, MDBValueStruct *V);
BOOL MDBodbcAdd(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
BOOL MDBodbcAddDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
BOOL MDBodbcRemove(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
BOOL MDBodbcRemoveDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
BOOL MDBodbcClear(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
BOOL MDBodbcIsObject(const unsigned char *Object, MDBValueStruct *V);
BOOL MDBodbcGetObjectDetails(const unsigned char *Object, unsigned char *Type, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V);
BOOL MDBodbcGetObjectDetailsEx(const unsigned char *Object, MDBValueStruct *Types, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V);
BOOL MDBodbcVerifyPassword(const unsigned char *Object, const unsigned char *Password, MDBValueStruct *V);
BOOL MDBodbcChangePassword(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V);
BOOL MDBodbcChangePasswordEx(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V);
BOOL MDBodbcCreateAlias(const unsigned char *Alias, const unsigned char *AliasedObjectDn, MDBValueStruct *V);
BOOL MDBodbcCreateObject(const unsigned char *Object, const unsigned char *Class, MDBValueStruct *Attribute, MDBValueStruct *Data, MDBValueStruct *V);
BOOL MDBodbcDeleteObject(const unsigned char *Object, BOOL Recursive, MDBValueStruct *V);
static __inline BOOL MDBodbcDeleteObjectByID(signed long id, BOOL Recursive, MDBValueStruct *V);
BOOL MDBodbcRenameObject(const unsigned char *ObjectOld, const unsigned char *ObjectNew, MDBValueStruct *V);
BOOL MDBodbcGrantObjectRights(const unsigned char *Object, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Delete, BOOL Rename, BOOL Admin, MDBValueStruct *V);
BOOL MDBodbcGrantAttributeRights(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Admin, MDBValueStruct *V);

const unsigned char *MDBodbcListContainableClassesEx(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V);
const unsigned char *MDBodbcReadEx(const unsigned char *Object, const unsigned char *Attribute, MDBEnumStruct *E, MDBValueStruct *V);
const unsigned char *MDBodbcEnumerateAttributesEx(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V);
const unsigned char *MDBodbcEnumerateObjectsEx(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, unsigned long Flags, MDBEnumStruct *E, MDBValueStruct *V);

long MDBodbcRead(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
long MDBodbcEnumerateObjects(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, MDBValueStruct *V);

EXPORT BOOL MDBODBCInit(MDBDriverInitStruct *Init);
EXPORT void MDBODBCShutdown(void);

void PrintSQLError(MDBValueStruct *V, HSTMT stmt);
static __inline BOOL MDBodbcGetObjectID(const unsigned char *dn, SQLINTEGER *id, SQLINTEGER *class, BOOL *read, BOOL *write, BOOL *delete, BOOL *rename, MDBValueStruct *V);
static __inline BOOL MDBodbcGetObjectDN(unsigned char *dn, const unsigned long maxlength, const signed long id, BOOL relative, MDBValueStruct *V);
static __inline BOOL MDBodbcWriteAny(const unsigned char *Object, const unsigned char *Attribute, const unsigned char Type, const BOOL clear, const unsigned char *Value, MDBValueStruct *V);

/* Prototype for placeholder */
BOOL MWHandleNamedTemplate(void *ignored1, unsigned char *ignored2, void *ignored3);

#define PrepareStatement(stmt, sql, requireBindings, V)                                 \
{                                                                                       \
    (requireBindings) = FALSE;                                                          \
                                                                                        \
    if ((stmt) == SQL_NULL_HSTMT) {                                                     \
        signed long prepareRC;                                                          \
                                                                                        \
        prepareRC = SQLAllocHandle(SQL_HANDLE_STMT, (V)->handle->connection, &(stmt));  \
        if (prepareRC == SQL_SUCCESS || prepareRC == SQL_SUCCESS_WITH_INFO) {           \
            prepareRC = SQLPrepare((stmt), (sql), SQL_NTS);                             \
            (V)->statementCount++;                                                      \
        }                                                                               \
                                                                                        \
        if (prepareRC == SQL_SUCCESS || prepareRC == SQL_SUCCESS_WITH_INFO) {           \
            (requireBindings) = TRUE;                                                   \
        }                                                                               \
    }                                                                                   \
}

#define ReleaseStatement(V, stmt)                                                       \
{                                                                                       \
    if ((stmt) != SQL_NULL_HSTMT) {                                                     \
        (V)->statementCount--;                                                          \
        SQLFreeHandle(SQL_HANDLE_STMT, (stmt));                                         \
    }                                                                                   \
}

#ifdef DEBUG
#define ExecuteStatement(stmt) (printf("%s:%d\n", __FILE__, __LINE__) &&   \
    DebugExecuteStatement((stmt)))

static __inline SQLRETURN
DebugExecuteStatement(SQLHSTMT stmt)
{
    SQLRETURN rc = SQLExecute(stmt);
    if (rc < 0) {
        PrintSQLError(NULL, stmt);
    }

    return(rc);
}
#else
#define ExecuteStatement(stmt) SQLExecute((stmt))
#endif

#define BindLongInput(stmt, position, paramAddr)                                        \
{                                                                                       \
    SQLBindParameter((stmt), (position), SQL_PARAM_INPUT, SQL_C_LONG, SQL_INTEGER,      \
        0, 0, (paramAddr), 0, NULL);                                                    \
}

#define BindCharInput(stmt, position, stringAddr, lengthAddr, size)                     \
{                                                                                       \
    SQLBindParameter((stmt), (position), SQL_PARAM_INPUT, SQL_C_CHAR, SQL_CHAR,         \
        (size), 0, (stringAddr), 0, (lengthAddr));                                      \
}

#define BindLongResult(stmt, position, resultAddr)                                      \
{                                                                                       \
    SQLBindCol((stmt), (position), SQL_C_ULONG, (resultAddr), 0, NULL);                 \
}

#define BindCharResult(stmt, position, resultAddr, resultSize)                          \
{                                                                                       \
    SQLBindCol((stmt), (position), SQL_C_CHAR, &resultAddr, resultSize, NULL);          \
}

#define StoreCharInput(string, stringAddr, length, maxLength)                           \
{                                                                                       \
    (length) = strlen((string));                                                        \
    if ((length) >= (maxLength)) {                                                      \
        (length) = (maxLength);                                                         \
    }                                                                                   \
                                                                                        \
    strncpy((stringAddr), (string), (length));                                          \
    (stringAddr)[(length)] = '\0';                                                      \
}


#define PrintSQLErr(V, stmt)                                        \
{                                                                   \
    XplConsolePrintf("SQL Error\n   %s:%d\n", __FILE__, __LINE__);  \
                                                                    \
    PrintSQLError((V), (stmt));                                     \
}

void
PrintSQLError(MDBValueStruct *V, HSTMT stmt)
{
    SQLCHAR state[6];
    SQLCHAR msg[SQL_MAX_MESSAGE_LENGTH];
    SQLSMALLINT len;
    SQLINTEGER nativeError;
    SQLSMALLINT i;
    SQLRETURN rc;

    i = 1;
    if (MDBodbc.env != SQL_NULL_HENV) {
        while ((rc = SQLGetDiagRec(SQL_HANDLE_ENV, (SQLHANDLE)MDBodbc.env, i, state, &nativeError,
            msg, sizeof(msg), &len)) == SQL_SUCCESS) 
        {
            XplConsolePrintf("   SQLSTATE = %s\n", state);
            XplConsolePrintf("   NATIVE ERROR = %ld\n", nativeError);
            XplConsolePrintf("   MSG = %s\n\n", msg);

            i++;
        }
    }

    if (V && V->handle && V->handle->connection != SQL_NULL_HDBC) {
        i = 1;
        while ((rc = SQLGetDiagRec(SQL_HANDLE_DBC, (SQLHANDLE)V->handle->connection, i, state, &nativeError,
            msg, sizeof(msg), &len)) != SQL_NO_DATA) 
        {
            XplConsolePrintf("   SQLSTATE = %s\n", state);
            XplConsolePrintf("   NATIVE ERROR = %ld\n", nativeError);
            XplConsolePrintf("   MSG = %s\n\n", msg);

            i++;
        }
    }

    if (stmt != SQL_NULL_HSTMT) {
        i = 1;
        while ((rc = SQLGetDiagRec(SQL_HANDLE_STMT, (SQLHANDLE)stmt, i, state, &nativeError,
            msg, sizeof(msg), &len)) != SQL_NO_DATA) 
        {
            XplConsolePrintf("   SQLSTATE = %s\n", state);
            XplConsolePrintf("   NATIVE ERROR = %ld\n", nativeError);
            XplConsolePrintf("   MSG = %s\n\n", msg);

            i++;
        }
    }
}

static __inline BOOL
MDBodbcGetObjectID(const unsigned char *dn, SQLINTEGER *id, SQLINTEGER *class, BOOL *read, BOOL *write, BOOL *delete, BOOL *rename, MDBValueStruct *V)
{
    if (dn && id) {
        SQLINTEGER rc;
        BOOL requireBindings;
        BOOL r;
        BOOL w;
        BOOL d;
        BOOL n;
        unsigned long now = time(NULL);

        if (now <= V->cache.time + 5 && XplStrCaseCmp(dn, V->cache.dn) == 0) {
            /* The value is in the cache, and is still valid */
            if (read) {
                *read = V->cache.read;
            }

            if (write) {
                *write = V->cache.write;
            }

            if (delete) {
                *delete = V->cache.delete;
            }

            if (rename) {
                *rename = V->cache.rename;
            }

            if (class) {
                *class = V->cache.class;
            }

            *id = V->cache.id;

            return(TRUE);
        }

        PrepareStatement(V->stmts.getRootRights, SQL_GET_ROOT_RIGHTS, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.getRootRights, 1, &V->handle->object);

            BindLongResult(V->stmts.getRootRights, 1, &V->params.getRootRights.read);
            BindLongResult(V->stmts.getRootRights, 2, &V->params.getRootRights.write);
            BindLongResult(V->stmts.getRootRights, 3, &V->params.getRootRights.delete);
            BindLongResult(V->stmts.getRootRights, 4, &V->params.getRootRights.rename);
        }

        PrepareStatement(V->stmts.getObjectID, SQL_GET_OBJECT_ID, requireBindings, V);
        if (requireBindings) {
            BindCharInput(V->stmts.getObjectID, 1,
                &V->params.getObjectID.object, &V->params.getObjectID.objectLength, MDB_ODBC_NAME_SIZE);
            BindLongInput(V->stmts.getObjectID, 2, &V->params.getObjectID.id);
            BindLongInput(V->stmts.getObjectID, 3, &V->handle->object);

            /* Resulting object id */
            BindLongResult(V->stmts.getObjectID, 1, &V->params.getObjectID.id);
            BindLongResult(V->stmts.getObjectID, 2, &V->params.getObjectID.class);

            /* Rights if specified */
            BindLongResult(V->stmts.getObjectID, 3, &V->params.getObjectID.read);
            BindLongResult(V->stmts.getObjectID, 4, &V->params.getObjectID.write);
            BindLongResult(V->stmts.getObjectID, 5, &V->params.getObjectID.delete);
            BindLongResult(V->stmts.getObjectID, 6, &V->params.getObjectID.rename);
        }

        if (V->stmts.getObjectID != SQL_NULL_HSTMT) {
            unsigned char *s = (unsigned char *)dn;
            unsigned char *e;

            if (*s == '\\') {

                /*
                    Check for rights assigned to this object at the root.  If there are any, use those.  If not then
                    use the default, (ie readonly).
                */
                rc = ExecuteStatement(V->stmts.getRootRights);
                if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                    rc = SQLFetch(V->stmts.getRootRights);
                    SQLCloseCursor(V->stmts.getRootRights);
                }

                if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                    r = V->params.getRootRights.read;
                    w = V->params.getRootRights.write;
                    d = V->params.getRootRights.delete;
                    n = V->params.getRootRights.rename;
                } else {
                    /* Set the default rights */
                    r = TRUE;
                    w = FALSE;
                    d = FALSE;
                    n = FALSE;
                }

                V->params.getObjectID.id = MDB_ODBC_ROOT_ID;
                s++;
            } else {
                V->params.getObjectID.id = *V->base.id;

                /* Set the default rights */
                r = *V->base.read;
                w = *V->base.write;
                d = *V->base.delete;
                n = *V->base.rename;
            }

            do {
                while (*s == '\\') {
                    s++;
                }

                e = strchr(s, '\\');

                if (*s) {
                    V->params.getObjectID.objectLength = (e) ? (unsigned long) (e - s) : strlen(s);
                    if (V->params.getObjectID.objectLength >= MDB_ODBC_NAME_SIZE) {
                        V->params.getObjectID.objectLength = MDB_ODBC_NAME_SIZE;
                    }

                    strncpy(V->params.getObjectID.object, s, V->params.getObjectID.objectLength);
                    V->params.getObjectID.object[V->params.getObjectID.objectLength] = '\0';
                } else {
                    V->params.getObjectID.objectLength = strlen(MDBodbc.config.treename);
                    strcpy(V->params.getObjectID.object, MDBodbc.config.treename);
                }

                /* Ok, we're all setup - Do the query */
                rc = ExecuteStatement(V->stmts.getObjectID);
                if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                    rc = SQLFetch(V->stmts.getObjectID);
                    SQLCloseCursor(V->stmts.getObjectID);

                    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                        if (V->params.getObjectID.read >= 0) {
                            r = (V->params.getObjectID.read == 1);
                        }

                        if (V->params.getObjectID.write >= 0) {
                            w = (V->params.getObjectID.write == 1);
                        }

                        if (V->params.getObjectID.delete >= 0) {
                            d = (V->params.getObjectID.delete == 1);
                        }

                        if (V->params.getObjectID.rename >= 0) {
                            n = (V->params.getObjectID.rename == 1);
                        }
                    }
                }

                s = e;
            } while (s && *(s++) && (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO));

            if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                *id = V->params.getObjectID.id;

                if (class) {
                    *class = V->params.getObjectID.class;
                }

                if (read) {
                    *read = r;
                }

                if (write) {
                    *write = w;
                }

                if (delete) {
                    *delete = d;
                }

                if (rename) {
                    *rename = n;
                }

                /* Store the cached value */
                V->cache.read = r;
                V->cache.write = w;
                V->cache.delete = d;
                V->cache.rename = n;

                V->cache.id = *id;
                V->cache.class = V->params.getObjectID.class;
                V->cache.time = now;

                strcpy(V->cache.dn, dn);

                return(TRUE);
            }
        }
    }

    V->ErrNo = ERR_NO_SUCH_ENTRY;

    return(FALSE);
}

static __inline BOOL MDBodbcGetObjectDN(unsigned char *dn, const unsigned long maxlength, const signed long id, BOOL relative, MDBValueStruct *V)
{
    unsigned char *s = dn;
    signed long i;
    signed long u;
    BOOL requireBindings;
    BOOL result = TRUE;

    if (!dn || !V) {
        return(FALSE);
    }

    u = (signed long) V->Used;

    PrepareStatement(V->stmts.getParentInfo, SQL_GET_PARENT_INFO, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.getParentInfo, 1, &V->params.getParentInfo.object);
        BindLongResult(V->stmts.getParentInfo, 1, &V->params.getParentInfo.object);
        BindCharResult(V->stmts.getParentInfo, 2, V->params.getParentInfo.name, MDB_ODBC_NAME_SIZE);
    }

    if (V->stmts.getParentInfo == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.getParentInfo.object = id;

    do {
        SQLINTEGER rc;

        /* Ok, do it */
        rc = ExecuteStatement(V->stmts.getParentInfo);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(V->stmts.getParentInfo);
            SQLCloseCursor(V->stmts.getParentInfo);
        }

        if (V->params.getParentInfo.object == id) {
            /* We need to be sure we are not looping */
            break;
        }

        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            MDBodbcAddValue(V->params.getParentInfo.name, V);
        } else {
            result = FALSE;
            break;
        }
    } while (V->params.getParentInfo.object != -1 && (!relative || V->params.getParentInfo.object != *(V->base.id)));

    /* If relative is true, then we can be relative even to the root */
    if (V->params.getParentInfo.object == -1 && !relative) {
        s += sprintf(s, "\\%s\\", MDBodbc.config.treename);
    }

    if (result) {
        for (i = V->Used - 1; i >= u ; i--) {
            if ((s - dn) + strlen(V->Value[i]) < maxlength) {
                s += sprintf(s, "%s\\", V->Value[i]);
            } else {
                result = FALSE;
                break;
            }
        }
    }

    if (result) {
        /* Cut off the trailing slash */
        *--s = '\0';
    }

    while (V->Used > (unsigned long)u) {
        MDBodbcFreeValue(u, V);
    }

    return(result);
}

static __inline BOOL
MDBodbcRemoveInheritedRights(SQLINTEGER trustee, BOOL first, SQLINTEGER provider, MDBValueStruct *V)
{
    SQLHSTMT getChildrenIDs = SQL_NULL_HSTMT;
    SQLINTEGER child;
    SQLINTEGER rc;
    BOOL requireBindings;

    if (first) {
        PrepareStatement(V->stmts.getRightsProviders, SQL_GET_RIGHTS_PROVIDERS, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.getRightsProviders, 1, &V->params.getRightsProviders.trustee);
            BindLongResult(V->stmts.getRightsProviders, 1, &V->params.getRightsProviders.provider);
        }

        V->params.getRightsProviders.trustee = trustee;

        rc = ExecuteStatement(V->stmts.getRightsProviders);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(V->stmts.getRightsProviders);

            while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                if (!MDBodbcRemoveInheritedRights(trustee, FALSE, V->params.getRightsProviders.provider, V)) {
                    SQLCloseCursor(V->stmts.getRightsProviders);
                    return(FALSE);
                }

                rc = SQLFetch(V->stmts.getRightsProviders);
            }
            SQLCloseCursor(V->stmts.getRightsProviders);
        }
    } else {
        PrepareStatement(V->stmts.delRightsByChild, SQL_DEL_RIGHTS_BY_CHILD, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.delRightsByChild, 1, &V->params.delRightsByChild.provider);
            BindLongInput(V->stmts.delRightsByChild, 2, &V->params.delRightsByChild.trustee);
        }

        V->params.delRightsByChild.provider = provider;
        V->params.delRightsByChild.trustee = trustee;

        rc = ExecuteStatement(V->stmts.delRightsByChild);
        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
            return(FALSE);
        }

        /* Get the children */
        PrepareStatement(getChildrenIDs, SQL_GET_CHILDREN_IDS, requireBindings, V);
        if (requireBindings) {
            BindLongInput(getChildrenIDs, 1, &trustee);
            BindLongResult(getChildrenIDs, 1, &child);
        }

        if (getChildrenIDs == SQL_NULL_HSTMT) {
            return(FALSE);
        }

        rc = ExecuteStatement(getChildrenIDs);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(getChildrenIDs);
        }

        while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            if (!MDBodbcRemoveInheritedRights(child, FALSE, provider, V)) {
                ReleaseStatement(V, getChildrenIDs);
                return(FALSE);
            }

            rc = SQLFetch(getChildrenIDs);
        }

        ReleaseStatement(V, getChildrenIDs);
        if (rc != SQL_NO_DATA) {
            return(FALSE);
        }
    }

    return(TRUE);
}

static __inline BOOL
MDBodbcCopyObjectRights(SQLINTEGER trustee, SQLINTEGER parent, BOOL fromParent, MDBValueStruct *V)
{
    SQLHSTMT getChildrenIDs = SQL_NULL_HSTMT;
    SQLINTEGER child;
    SQLINTEGER rc;
    BOOL requireBindings;

    PrepareStatement(V->stmts.setRights, SQL_SET_RIGHTS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.setRights, 1, &V->params.setGetRights.object);
        BindLongInput(V->stmts.setRights, 2, &V->params.setGetRights.trustee);
        BindLongInput(V->stmts.setRights, 3, &V->params.setGetRights.provider);
        BindLongInput(V->stmts.setRights, 4, &V->params.setGetRights.parent);
        BindLongInput(V->stmts.setRights, 5, &V->params.setGetRights.read);
        BindLongInput(V->stmts.setRights, 6, &V->params.setGetRights.write);
        BindLongInput(V->stmts.setRights, 7, &V->params.setGetRights.delete);
        BindLongInput(V->stmts.setRights, 8, &V->params.setGetRights.rename);
    }

    PrepareStatement(V->stmts.getRights, SQL_GET_RIGHTS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.getRights, 1, &V->params.setGetRights.trustee);

        BindLongResult(V->stmts.getRights, 1, &V->params.setGetRights.provider);
        BindLongResult(V->stmts.getRights, 2, &V->params.setGetRights.object);
        BindLongResult(V->stmts.getRights, 3, &V->params.setGetRights.read);
        BindLongResult(V->stmts.getRights, 4, &V->params.setGetRights.write);
        BindLongResult(V->stmts.getRights, 5, &V->params.setGetRights.delete);
        BindLongResult(V->stmts.getRights, 6, &V->params.setGetRights.rename);
    }

    V->params.setGetRights.trustee = parent;

    /*
        Get each set of rights assigned to the parent, and add a copy to each
        child.  This has to be done as seperate queries because some ODBC drivers
        do not support INSERT using data from a SELECT that uses the same table.
    */
    rc = ExecuteStatement(V->stmts.getRights);
    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(V->stmts.getRights);
    }

    while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        V->params.setGetRights.trustee = trustee;
        V->params.setGetRights.parent = fromParent ? 1 : 0;

        rc = ExecuteStatement(V->stmts.setRights);

        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(V->stmts.getRights);
        }
    }

    SQLCloseCursor(V->stmts.getRights);

    /* Do the same for all the children */
    PrepareStatement(getChildrenIDs, SQL_GET_CHILDREN_IDS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(getChildrenIDs, 1, &trustee);
        BindLongResult(getChildrenIDs, 1, &child);
    }

    if (getChildrenIDs == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    rc = ExecuteStatement(getChildrenIDs);
    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(getChildrenIDs);
    }

    while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        if (!MDBodbcCopyObjectRights(child, trustee, TRUE, V)) {
            ReleaseStatement(V, getChildrenIDs);
            return(FALSE);
        }

        rc = SQLFetch(getChildrenIDs);
    }

    ReleaseStatement(V, getChildrenIDs);
    if (rc != SQL_NO_DATA) {
        return(FALSE);
    }

    return(TRUE);
}

BOOL 
MDBodbcGetServerInfo(unsigned char *ServerDN, unsigned char *ServerTree, MDBValueStruct *V)
{
    signed long server;
    MDBHandle handle = NULL;
    MDBValueStruct *v;
    BOOL result = TRUE;

    if (!V) {
        handle = MDBodbcAuthenticate(NULL, NULL, NULL);
        v = (handle) ? MDBodbcCreateValueStruct(handle, NULL) : NULL;
    } else {
        v = V;
    }

    if (!v) {
        if (handle) {
            MDBodbcRelease(handle);
        }

        return(FALSE);
    }

    if (ServerTree) {
        strcpy(ServerTree, MDBodbc.config.treename);
    }

    if (ServerDN) {
        unsigned long base = *(v->base.id);
        *(v->base.id) = MDBodbc.config.defaultContext;

        result = FALSE;
        if (MDBodbcGetObjectID(MDBodbc.config.servername, &server, NULL, NULL, NULL, NULL, NULL, v)) {
            *(v->base.id) = -1;

            if (MDBodbcGetObjectDN(ServerDN, MDB_MAX_OBJECT_CHARS, server, FALSE, v)) {
                result = TRUE;
            }
        }

        *(v->base.id) = base;
        
    }

    if (handle) {
        MDBodbcDestroyValueStruct(v);
        MDBodbcRelease(handle);
    }

    return(result);
}

char * 
MDBodbcGetBaseDN(MDBValueStruct *V)
{
    return(MDBodbc.config.treename);
}


MDBHandle
MDBodbcAuthenticate(const unsigned char *Object, const unsigned char *Password, const unsigned char *Arguments)
{
    BOOL result = TRUE;

    MDBHandle h = malloc(sizeof(MDBodbcContextStruct));

    if (h) {
        SQLINTEGER rc;

        memset(h, 0, sizeof(MDBodbcContextStruct));

        rc = SQLAllocHandle(SQL_HANDLE_DBC, MDBodbc.env, &h->connection);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            SQLSetConnectAttr(h->connection, SQL_LOGIN_TIMEOUT, (SQLPOINTER *)30, 0);
            SQLSetConnectAttr(h->connection, SQL_ATTR_AUTOCOMMIT, SQL_AUTOCOMMIT_OFF, 0);

            /* Connect to the datasource */
            rc = SQLConnect(h->connection,  (SQLCHAR *) MDBodbc.config.dsn,  SQL_NTS,
                                            (SQLCHAR *) MDBodbc.config.user, SQL_NTS,
                                            (SQLCHAR *) MDBodbc.config.pass, SQL_NTS);

            if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                /* We now have a connection */
                if (Object) {
                    MDBValueStruct *V = MDBodbcCreateValueStruct(h, NULL);

                    if (!MDBodbcGetObjectID(Object, &h->object, NULL, NULL, NULL, NULL, NULL, V)) {
                        /* The object doesn't exist here, lets try again in the default container */

                        *(V->base.id) = MDBodbc.config.defaultContext;
                        if (*Object != '\\' || !MDBodbcGetObjectID(Object, &h->object, NULL, NULL, NULL, NULL, NULL, V)) {
                            /* The object doesn't exist */
                            result = FALSE;
                        }
                    }

                    if (result && !MDBodbcVerifyPassword(Object, Password, V)) {
                        /* Wrong password */
                        result = FALSE;
                    }

                    MDBodbcDestroyValueStruct(V);
                }

                if (result) {
                    return(h);
                }
            }

            SQLDisconnect(h->connection);
        }

        free(h);
        h = NULL;
    }

    return(NULL);
}

BOOL
MDBodbcRelease(MDBHandle Context)
{
    if (Context) {
        SQLEndTran(SQL_HANDLE_DBC, Context->connection, SQL_COMMIT);
        SQLDisconnect(Context->connection);
        SQLFreeHandle(SQL_HANDLE_DBC, Context->connection);

        free(Context);
        return(TRUE);
    } else {
        return(FALSE);
    }
}

MDBValueStruct *
MDBodbcCreateValueStruct(MDBHandle Handle, const unsigned char *Context)
{
    if (Handle) {
        MDBValueStruct *v = malloc(sizeof(MDBValueStruct));

        if (v) {
            memset(v, 0, sizeof(MDBValueStruct));

            v->handle = Handle;

            v->base.id = &(v->base.value);
            v->base.class = &(v->base.classValue);
            v->base.read = &(v->base.readValue);
            v->base.write = &(v->base.writeValue);
            v->base.delete = &(v->base.deleteValue);
            v->base.rename = &(v->base.renameValue);

            MDBodbcSetValueStructContext(Context, v);

            /* Everything seems to have worked */
            return(v);
        }
    }

    return(NULL);
}

BOOL 
MDBodbcFreeValues(MDBValueStruct *V)
{
    register unsigned long i;

    if (V->Allocated) {
        for (i = 0; i < V->Used; i++) {
            free(V->Value[i]);
        }

        if (V->Allocated > VALUE_ALLOC_SIZE) {
            if (V->Value) {
                free(V->Value);
            }

            V->Allocated = 0;
            V->Value = NULL;
        }

        V->Used = 0;
    }

    return(TRUE);
}

BOOL 
MDBodbcDestroyValueStruct(MDBValueStruct *V)
{
    if (V) {
        unsigned long i;

        for (i = 0; i < (sizeof(V->stmts) / sizeof(SQLHSTMT)); i++) {
            ReleaseStatement(V, V->stmts.asArray[i]);
        }

        MDBodbcFreeValues(V);
        if (V->Value) {
            free(V->Value);
        }

        if (V->statementCount) {
            XplConsolePrintf("This value struct still has %ld statement handles\n", V->statementCount);
        }

        free(V);
        return(TRUE);
    } else {
        return(FALSE);
    }
}

MDBEnumStruct *
MDBodbcCreateEnumStruct(MDBValueStruct *V)
{
    if (V) {
        MDBEnumStruct *e = malloc(sizeof(MDBEnumStruct));

        if (e) {
            memset(e, 0, sizeof(MDBEnumStruct));

            return(e);
        }
    }

    return(NULL);
}

BOOL
MDBodbcDestroyEnumStruct(MDBEnumStruct *E, MDBValueStruct *V)
{
    if (E) {
        ReleaseStatement(V, E->stmt);

        if (E->largeResult) {
            free(E->largeResult);
        }

        free(E);
        return(TRUE);
    } else {
        return(FALSE);
    }
}

BOOL
MDBodbcSetValueStructContext(const unsigned char *Context, MDBValueStruct *V)
{
    if (V) {
        return(MDBodbcGetObjectID(Context ? Context : (unsigned char *)"\\", V->base.id, V->base.class, V->base.read, V->base.write, V->base.delete, V->base.rename, V));
    }

    return(FALSE);
}

MDBValueStruct *
MDBodbcShareContext(MDBValueStruct *V)
{
    if (V) {
        MDBValueStruct *v = malloc(sizeof(MDBValueStruct));

        if (v) {
            memset(v, 0, sizeof(MDBValueStruct));

            v->base.id = V->base.id;
            v->base.class = V->base.class;
            v->base.read = V->base.read;
            v->base.write = V->base.write;
            v->base.delete = V->base.delete;
            v->base.rename = V->base.rename;

            v->handle = V->handle;

            return(v);
        }
    }

    return(NULL);
}

BOOL
MDBodbcAddValue(const unsigned char *Value, MDBValueStruct *V)
{
    register unsigned char *ptr;

    if (Value && (*Value != '\0')) {
        if ((V->Used + 1) > V->Allocated) {
            ptr = (unsigned char *)realloc(V->Value, (V->Allocated + VALUE_ALLOC_SIZE) * sizeof(unsigned char *));
            if (ptr) {
                V->Value = (unsigned char    **)ptr;
                V->Allocated += VALUE_ALLOC_SIZE;
            } else {
                if (V->Allocated) {
                    MDBodbcFreeValues(V);

                    if (V->Value) {
                        free(V->Value);
                    }
                }

                V->Value = NULL;

                V->Used = 0;
                V->Allocated = 0;

                return(FALSE);
            }
        }

        ptr = strdup(Value);
        if (ptr) {
            V->Value[V->Used] = ptr;
            V->Used++;

            return(TRUE);
        }
    }

    return(FALSE);
}

BOOL
MDBodbcFreeValue(unsigned long Index, MDBValueStruct *V)
{
    if (Index < V->Used) {
        free(V->Value[Index]);

        if (Index < (V->Used - 1)) {
            memmove(&V->Value[Index], &V->Value[Index + 1], ((V->Used - 1) - Index) * sizeof(unsigned char *));
        }

        V->Used--;

        return(TRUE);
    }

    return(FALSE);
}

BOOL 
MDBodbcDefineAttribute(const unsigned char *Attribute, const unsigned char *ASN1, unsigned long Type, BOOL Single, BOOL ImmediateSync, BOOL Public, MDBValueStruct *V)
{
    SQLINTEGER root;
    BOOL allowed;

    if (!MDBodbcGetObjectID("\\", &root, NULL, NULL, &allowed, NULL, NULL, V) || !allowed) {
        return(FALSE);
    }

    if (Attribute) {
        SQLINTEGER rc;
        BOOL requireBindings;

        PrepareStatement(V->stmts.createAttribute, SQL_CREATE_ATTRIBUTE, requireBindings, V);
        if (requireBindings) {
            BindCharInput(V->stmts.createAttribute, 1, V->params.createAttribute.name, &V->params.createAttribute.nameLength, MDB_ODBC_NAME_SIZE);
            BindLongInput(V->stmts.createAttribute, 2, &V->params.createAttribute.type);
            BindLongInput(V->stmts.createAttribute, 3, &V->params.createAttribute.single);
            BindLongInput(V->stmts.createAttribute, 4, &V->params.createAttribute.public);
        }

        if (V->stmts.createAttribute != SQL_NULL_HSTMT) {
            StoreCharInput(Attribute, V->params.createAttribute.name, V->params.createAttribute.nameLength, MDB_ODBC_NAME_SIZE);

            switch (Type) {
                case MDB_ATTR_SYN_BOOL:
                case MDB_ATTR_SYN_DIST_NAME:
                case MDB_ATTR_SYN_BINARY:
                case MDB_ATTR_SYN_STRING: {
                    V->params.createAttribute.type = Type;
                    break;
                }

                default: {
                    /* Unknown Type */
                    V->params.createAttribute.type = 0;
                    break;
                }
            }

            V->params.createAttribute.single = Single ? 1 : 0;
            V->params.createAttribute.public = Public ? 1 : 0;

            /* Ok, we're ready */
            rc = ExecuteStatement(V->stmts.createAttribute);
            if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
                return(TRUE);
            }
        }
    }

    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
    return(FALSE);
}

static __inline BOOL 
MDBodbcAddAttributeByID(const unsigned char *Attribute, unsigned long class, MDBValueStruct *V)
{
    SQLINTEGER rc;
    BOOL requireBindings;
    SQLHSTMT stmt = SQL_NULL_HSTMT;
    unsigned long subclass;
    SQLINTEGER root;
    BOOL allowed;

    if (!MDBodbcGetObjectID("\\", &root, NULL, NULL, &allowed, NULL, NULL, V) || !allowed) {
        return(FALSE);
    }

    /* Get all subclasses and call add attribute on them as well (recursively) */
    PrepareStatement(stmt, SQL_GET_SUBCLASSES, requireBindings, V);
    if (requireBindings) {
        BindLongInput(stmt, 1, &class);
        BindLongResult(stmt, 1, &subclass);
    }

    if (stmt == SQL_NULL_HSTMT) {
        ReleaseStatement(V, stmt);
        return(FALSE);
    }

    rc = ExecuteStatement(stmt);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
        ReleaseStatement(V, stmt);
        return(FALSE);
    }

    rc = SQLFetch(stmt);
    while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        MDBodbcAddAttributeByID(Attribute, subclass, V);

        rc = SQLFetch(stmt);
    }

    if (rc != SQL_NO_DATA) {
        ReleaseStatement(V, stmt);
        return(FALSE);
    }

    ReleaseStatement(V, stmt);

    PrepareStatement(V->stmts.createOptional, SQL_CREATE_OPTIONAL, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.createOptional, 1, &V->params.createOptional.class);
        BindCharInput(V->stmts.createOptional, 2, V->params.createOptional.name, &V->params.createOptional.nameLength, MDB_ODBC_NAME_SIZE);
    }

    if (V->stmts.createOptional == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.createOptional.class = class;
    StoreCharInput(Attribute, V->params.createOptional.name, V->params.createOptional.nameLength, MDB_ODBC_NAME_SIZE);

    rc = ExecuteStatement(V->stmts.createOptional);
    return(rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO);
}

BOOL 
MDBodbcAddAttribute(const unsigned char *Attribute, const unsigned char *Class, MDBValueStruct *V)
{
    SQLINTEGER rc;
    BOOL requireBindings;
    SQLINTEGER root;
    BOOL allowed;

    if (!MDBodbcGetObjectID("\\", &root, NULL, NULL, &allowed, NULL, NULL, V) || !allowed) {
        return(FALSE);
    }

    PrepareStatement(V->stmts.getClassByName, SQL_GET_CLASS_BY_NAME, requireBindings, V);
    if (requireBindings) {
        BindCharInput(V->stmts.getClassByName, 1, V->params.getClassByName.name, &V->params.getClassByName.nameLength, MDB_ODBC_NAME_SIZE);
        BindLongResult(V->stmts.getClassByName, 1, &V->params.getClassByName.class);
    }

    if (V->stmts.getClassByName == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    StoreCharInput(Class, V->params.getClassByName.name, V->params.getClassByName.nameLength, MDB_ODBC_NAME_SIZE);

    rc = ExecuteStatement(V->stmts.getClassByName);
    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(V->stmts.getClassByName);
        SQLCloseCursor(V->stmts.getClassByName);

        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
        return(MDBodbcAddAttributeByID(Attribute, V->params.getClassByName.class, V));
    } else {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }
}

BOOL 
MDBodbcUndefineAttribute(const unsigned char *Attribute, MDBValueStruct *V)
{
    SQLINTEGER attribute;
    SQLINTEGER rc;
    BOOL requireBindings;
    SQLINTEGER root;
    BOOL allowed;

    if (!MDBodbcGetObjectID("\\", &root, NULL, NULL, &allowed, NULL, NULL, V) || !allowed) {
        return(FALSE);
    }

    if (!Attribute || !V) {
        return(FALSE);
    }

    /* Get the attribute ID */
        BindLongResult(V->stmts.verifyPassword, 1, &V->params.verifyPassword.object);
    PrepareStatement(V->stmts.getAttrByName, SQL_GET_ATTRIBUTE_BY_NAME, requireBindings, V);
    if (requireBindings) {
        BindCharInput(V->stmts.getAttrByName, 1,
            V->params.getAttrByName.attribute, &V->params.getAttrByName.attributeLength, MDB_ODBC_NAME_SIZE);
        BindLongResult(V->stmts.getAttrByName, 1, &V->params.getAttrByName.id);
    }

    if (V->stmts.getAttrByName == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    StoreCharInput(Attribute, V->params.getAttrByName.attribute, V->params.getAttrByName.attributeLength, MDB_ODBC_NAME_SIZE);

    /* Ok, we're all setup - Do the query */
    rc = ExecuteStatement(V->stmts.getAttrByName);

    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(V->stmts.getAttrByName);
        SQLCloseCursor(V->stmts.getAttrByName);
        attribute = V->params.getAttrByName.id;
    }

    /* Remove all references to the attribute */
    PrepareStatement(V->stmts.deleteAttrFromMandatory, SQL_MANDATORY_DELETE_ATTR, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteAttrFromMandatory, 1, &V->params.deleteAttrFromMandatory.attribute);
    }

    if (V->stmts.deleteAttrFromMandatory == SQL_NULL_HSTMT) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    V->params.deleteAttrFromMandatory.attribute = attribute;

    rc = ExecuteStatement(V->stmts.deleteAttrFromMandatory);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }


    PrepareStatement(V->stmts.deleteAttrFromOptional, SQL_OPTIONAL_DELETE_ATTR, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteAttrFromOptional, 1, &V->params.deleteAttrFromOptional.attribute);
    }

    if (V->stmts.deleteAttrFromOptional == SQL_NULL_HSTMT) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    V->params.deleteAttrFromOptional.attribute = attribute;

    rc = ExecuteStatement(V->stmts.deleteAttrFromOptional);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }


    PrepareStatement(V->stmts.deleteAttrFromNaming, SQL_NAMING_DELETE_ATTR, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteAttrFromNaming, 1, &V->params.deleteAttrFromNaming.attribute);
    }

    if (V->stmts.deleteAttrFromNaming == SQL_NULL_HSTMT) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    V->params.deleteAttrFromNaming.attribute = attribute;

    rc = ExecuteStatement(V->stmts.deleteAttrFromNaming);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    PrepareStatement(V->stmts.deleteAttrFromValues, SQL_VALUES_DELETE_ATTR, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteAttrFromValues, 1, &V->params.deleteAttrFromValues.attribute);
    }

    if (V->stmts.deleteAttrFromValues == SQL_NULL_HSTMT) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    V->params.deleteAttrFromValues.attribute = attribute;

    rc = ExecuteStatement(V->stmts.deleteAttrFromValues);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }


    /* Actually remove the attribute */
    PrepareStatement(V->stmts.deleteAttr, SQL_DELETE_ATTRIBUTE, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteAttr, 1, &V->params.deleteAttr.attribute);
    }

    if (V->stmts.deleteAttr == SQL_NULL_HSTMT) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    V->params.deleteAttr.attribute = attribute;

    rc = ExecuteStatement(V->stmts.deleteAttr);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
    return(TRUE);
}

/* This driver does not supprt/use the ASN1, or naming fields of a class */
BOOL 
MDBodbcDefineClass(const unsigned char *Class, const unsigned char *ASN1, BOOL Container, MDBValueStruct *Superclass, MDBValueStruct *Containment, MDBValueStruct *Naming, MDBValueStruct *Mandatory, MDBValueStruct *Optional, MDBValueStruct *V)
{
    unsigned long class;
    unsigned long i;
    BOOL result = TRUE;
    SQLINTEGER rc;
    BOOL requireBindings;
    SQLINTEGER root;
    BOOL allowed;

    if (!MDBodbcGetObjectID("\\", &root, NULL, NULL, &allowed, NULL, NULL, V) || !allowed) {
        return(FALSE);
    }

    /* Add the class */
    PrepareStatement(V->stmts.createClass, SQL_CREATE_CLASS, requireBindings, V);
    if (requireBindings) {
        BindCharInput(V->stmts.createClass, 1, V->params.createClass.name, &V->params.createClass.nameLength, MDB_ODBC_NAME_SIZE);
        BindLongInput(V->stmts.createClass, 2, &V->params.createClass.container);
    }

    if (V->stmts.createClass == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    StoreCharInput(Class, V->params.createClass.name, V->params.createClass.nameLength, MDB_ODBC_NAME_SIZE);
    V->params.createClass.container = Container ? 1 : 0;

    rc = ExecuteStatement(V->stmts.createClass);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
        /* The class is already there */

        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(TRUE);
    }

    /* Get the ID of the newly created class */
    PrepareStatement(V->stmts.getClassByName, SQL_GET_CLASS_BY_NAME, requireBindings, V);
    if (requireBindings) {
        BindCharInput(V->stmts.getClassByName, 1, V->params.getClassByName.name, &V->params.getClassByName.nameLength, MDB_ODBC_NAME_SIZE);
        BindLongResult(V->stmts.getClassByName, 1, &V->params.getClassByName.class);
    }

    if (V->stmts.getClassByName == SQL_NULL_HSTMT) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    StoreCharInput(Class, V->params.getClassByName.name, V->params.getClassByName.nameLength, MDB_ODBC_NAME_SIZE);

    rc = ExecuteStatement(V->stmts.getClassByName);
    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(V->stmts.getClassByName);
        SQLCloseCursor(V->stmts.getClassByName);

        class = V->params.getClassByName.class;
    } else {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    /* Assign the Containment */
    PrepareStatement(V->stmts.createContainment, SQL_CREATE_CONTAINMENT, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.createContainment, 1, &V->params.createContainment.class);
        BindCharInput(V->stmts.createContainment, 2, V->params.createContainment.name, &V->params.createContainment.nameLength, MDB_ODBC_NAME_SIZE);
    }

    if (V->stmts.createContainment == SQL_NULL_HSTMT) {
        result = FALSE;
    }

    if (result && Containment) {
        V->params.createContainment.class = class;

        for (i = 0; i < Containment->Used; i++) {
            StoreCharInput(Containment->Value[i], V->params.createContainment.name, V->params.createContainment.nameLength, MDB_ODBC_NAME_SIZE);

            rc = ExecuteStatement(V->stmts.createContainment);
            if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                result = FALSE;
            }
        }
    }

    PrepareStatement(V->stmts.createMandatory, SQL_CREATE_MANDATORY, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.createMandatory, 1, &V->params.createMandatory.class);
        BindCharInput(V->stmts.createMandatory, 2, V->params.createMandatory.name, &V->params.createMandatory.nameLength, MDB_ODBC_NAME_SIZE);
    }

    if (result && Mandatory) {
        V->params.createMandatory.class = class;

        for (i = 0; i < Mandatory->Used; i++) {
            BOOL create = TRUE;

            /* Naming attributes are always provided by the object name, so there is no need to add them to the mandatory list.  */
            if (Naming) {
                unsigned long n;

                for (n = 0; n < Naming->Used; n++) {
                    if (XplStrCaseCmp(Naming->Value[n], Mandatory->Value[i]) == 0) {
                        create = FALSE;
                        break;
                    }
                }
            }

            if (create) {
                StoreCharInput(Mandatory->Value[i], V->params.createMandatory.name, V->params.createMandatory.nameLength, MDB_ODBC_NAME_SIZE);

                rc = ExecuteStatement(V->stmts.createMandatory);
                if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                    result = FALSE;
                }
            }
        }
    }


    /* Assign the Optional Attributes */
    PrepareStatement(V->stmts.createOptional, SQL_CREATE_OPTIONAL, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.createOptional, 1, &V->params.createOptional.class);
        BindCharInput(V->stmts.createOptional, 2, V->params.createOptional.name, &V->params.createOptional.nameLength, MDB_ODBC_NAME_SIZE);
    }

    if (V->stmts.createOptional == SQL_NULL_HSTMT) {
        result = FALSE;
    }

    if (result) {
        V->params.createOptional.class = class;

        if (Optional) {
            for (i = 0; i < Optional->Used; i++) {
                StoreCharInput(Optional->Value[i], V->params.createOptional.name, V->params.createOptional.nameLength, MDB_ODBC_NAME_SIZE);

                rc = ExecuteStatement(V->stmts.createOptional);
                if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                    result = FALSE;
                }
            }
        }

        /* We add mandatory attributes as optional as well, so that we only have to check one list in MDBWrite */
        if (Mandatory) {
            for (i = 0; i < Mandatory->Used; i++) {
                StoreCharInput(Mandatory->Value[i], V->params.createOptional.name, V->params.createOptional.nameLength, MDB_ODBC_NAME_SIZE);

                rc = ExecuteStatement(V->stmts.createOptional);
                if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                    result = FALSE;
                }
            }
        }
    }

    /* Assign the Naming Attributes */
    PrepareStatement(V->stmts.createNaming, SQL_CREATE_NAMING, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.createNaming, 1, &V->params.createNaming.class);
        BindCharInput(V->stmts.createNaming, 2, V->params.createNaming.name, &V->params.createNaming.nameLength, MDB_ODBC_NAME_SIZE);
    }

    if (V->stmts.createNaming == SQL_NULL_HSTMT) {
        result = FALSE;
    }

    if (result && Naming) {
        V->params.createNaming.class = class;

        for (i = 0; i < Naming->Used; i++) {
            StoreCharInput(Naming->Value[i], V->params.createNaming.name, V->params.createNaming.nameLength, MDB_ODBC_NAME_SIZE);

            rc = ExecuteStatement(V->stmts.createNaming);
            if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                result = FALSE;
            }
        }
    }

    /*
        Assign the super classes

        This must be done before we do the copying, but we have to be sure to not copy the superclasses
        until last, to prevent getting duplicate values.
    */
    PrepareStatement(V->stmts.createSuperClass, SQL_CREATE_SUPERCLASS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.createSuperClass, 1, &V->params.createSuperClass.class);
        BindCharInput(V->stmts.createSuperClass, 2, V->params.createSuperClass.name, &V->params.createSuperClass.nameLength, MDB_ODBC_NAME_SIZE);
    }

    if (V->stmts.createSuperClass == SQL_NULL_HSTMT) {
        result = FALSE;
    }

    if (result && Superclass) {
        V->params.createSuperClass.class = class;

        for (i = 0; i < Superclass->Used; i++) {
            StoreCharInput(Superclass->Value[i], V->params.createSuperClass.name, V->params.createSuperClass.nameLength, MDB_ODBC_NAME_SIZE);

            rc = ExecuteStatement(V->stmts.createSuperClass);
            if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                result = FALSE;
            }
        }
    }

    /*
        Now that we have assigned all the values we need, we need to copy all the same things
        from the superclasses.
    */
    if (!Containment || Containment->Used == 0) {
        /* Containment is only inherited if it is not specified for a class */

        PrepareStatement(V->stmts.copyContainment, SQL_COPY_CONTAINMENT, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.copyContainment, 1, &V->params.copyContainment.class);
        }

        if (V->stmts.copyContainment == SQL_NULL_HSTMT) {
            result = FALSE;
        }

        if (result) {
            V->params.copyContainment.class = class;

            rc = ExecuteStatement(V->stmts.copyContainment);
            if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                result = FALSE;
            }
        }
    }

    PrepareStatement(V->stmts.copyOptional, SQL_COPY_OPTIONAL, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.copyOptional, 1, &V->params.copyOptional.class);
    }

    if (V->stmts.copyOptional == SQL_NULL_HSTMT) {
        result = FALSE;
    }

    if (result) {
        V->params.copyOptional.class = class;

        rc = ExecuteStatement(V->stmts.copyOptional);
        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
            result = FALSE;
        }
    }

    PrepareStatement(V->stmts.copyMandatory, SQL_COPY_MANDATORY, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.copyMandatory, 1, &V->params.copyMandatory.class);
    }

    if (V->stmts.copyMandatory == SQL_NULL_HSTMT) {
        result = FALSE;
    }

    if (result) {
        V->params.copyMandatory.class = class;

        rc = ExecuteStatement(V->stmts.copyMandatory);
        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
            result = FALSE;
        }
    }

    PrepareStatement(V->stmts.copyNaming, SQL_COPY_NAMING, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.copyNaming, 1, &V->params.copyNaming.class);
    }

    if (V->stmts.copyNaming == SQL_NULL_HSTMT) {
        result = FALSE;
    }

    if (result) {
        V->params.copyNaming.class = class;

        rc = ExecuteStatement(V->stmts.copyNaming);
        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
            result = FALSE;
        }
    }

    /*
        Copy the superclasses from the superclasses.  This has to be done after the copying
        because each class has the info from its superclass already, so if we do this before
        we'll get duplicate values.

        This has to be done though so that if an attribute is added to the superclass of a
        superclass at a later point that it can be added to this class as well.
    */
    PrepareStatement(V->stmts.copySuperClass, SQL_COPY_SUPERCLASSES, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.copySuperClass, 1, &V->params.copySuperClass.class);
        BindCharInput(V->stmts.copySuperClass, 2,
            V->params.copySuperClass.name, &V->params.copySuperClass.nameLength, MDB_ODBC_NAME_SIZE);
    }

    if (V->stmts.copySuperClass == SQL_NULL_HSTMT) {
        result = FALSE;
    }

    if (result && Superclass) {
        V->params.copySuperClass.class = class;

        for (i = 0; i < Superclass->Used; i++) {
            StoreCharInput(Superclass->Value[i], V->params.copySuperClass.name, V->params.copySuperClass.nameLength, MDB_ODBC_NAME_SIZE);

            rc = ExecuteStatement(V->stmts.copySuperClass);
            if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                result = FALSE;
            }
        }
    }

    /* Done */
    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, result ? SQL_COMMIT : SQL_ROLLBACK);
    return(result);
}

BOOL 
MDBodbcUndefineClass(const unsigned char *Class, MDBValueStruct *V)
{
    SQLINTEGER rc;
    BOOL requireBindings;
    SQLINTEGER root;
    BOOL allowed;

    if (!MDBodbcGetObjectID("\\", &root, NULL, NULL, &allowed, NULL, NULL, V) || !allowed) {
        return(FALSE);
    }

    if (!Class || !V) {
        return(FALSE);
    }

    /* Get the class ID */
    PrepareStatement(V->stmts.getClassByName, SQL_GET_CLASS_BY_NAME, requireBindings, V);
    if (requireBindings) {
        BindCharInput(V->stmts.getClassByName, 1,
            V->params.getClassByName.name, &V->params.getClassByName.nameLength, MDB_ODBC_NAME_SIZE);
        BindLongResult(V->stmts.getClassByName, 1, &V->params.getClassByName.class);
    }

    if (V->stmts.getClassByName == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    StoreCharInput(Class, V->params.getClassByName.name, V->params.getClassByName.nameLength, MDB_ODBC_NAME_SIZE);

    rc = ExecuteStatement(V->stmts.getClassByName);
    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(V->stmts.getClassByName);
        SQLCloseCursor(V->stmts.getClassByName);

        if (MDBodbcUndefineClassByID(V->params.getClassByName.class, V)) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
            return(TRUE);
        }
    }

    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
    return(FALSE);
}

static __inline BOOL 
MDBodbcUndefineClassByID(unsigned long class, MDBValueStruct *V)
{
    SQLINTEGER rc;
    BOOL requireBindings;
    SQLHSTMT stmt = SQL_NULL_HSTMT;
    unsigned long subclass;
    unsigned long object;

    /* Get all subclasses and call undefine class on them (recursive) */
    PrepareStatement(stmt, SQL_GET_SUBCLASSES, requireBindings, V);
    if (requireBindings) {
        BindLongInput(stmt, 1, &class);
        BindLongResult(stmt, 1, &subclass);
    }

    if (stmt == SQL_NULL_HSTMT) {
        ReleaseStatement(V, stmt);
        return(FALSE);
    }

    rc = ExecuteStatement(stmt);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
        ReleaseStatement(V, stmt);
        return(FALSE);
    }

    rc = SQLFetch(stmt);
    while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        MDBodbcUndefineClassByID(subclass, V);

        rc = SQLFetch(stmt);
    }

    if (rc != SQL_NO_DATA) {
        ReleaseStatement(V, stmt);
        return(FALSE);
    }

    ReleaseStatement(V, stmt);

    /* Get all objects of that type and delete recursively */
    stmt = SQL_NULL_HSTMT;
    PrepareStatement(stmt, SQL_GET_OBJECTS_BY_CLASS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(stmt, 1, &class);
        BindLongResult(stmt, 1, &object);
    }

    if (stmt == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    rc = ExecuteStatement(stmt);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
        return(FALSE);
    }

    rc = SQLFetch(stmt);
    while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        MDBodbcDeleteObjectByID(object, TRUE, V);

        rc = SQLFetch(stmt);
    }

    if (rc != SQL_NO_DATA) {
        return(FALSE);
    }

    ReleaseStatement(V, stmt);


    /* Remove from containment, mandatory, optional, and naming */
    PrepareStatement(V->stmts.deleteClassFromContainment, SQL_CONTAINMENT_DEL_CLASS, requireBindings, V);
    if (requireBindings) {
        /* Yes these are supposed to be the same */
        BindLongInput(V->stmts.deleteClassFromContainment, 1, &V->params.deleteClassFromContainment.class);
        BindLongInput(V->stmts.deleteClassFromContainment, 2, &V->params.deleteClassFromContainment.class);
    }

    if (V->stmts.deleteClassFromContainment == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.deleteClassFromContainment.class = class;

    rc = ExecuteStatement(V->stmts.deleteClassFromContainment);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        return(FALSE);
    }


    PrepareStatement(V->stmts.deleteClassFromMandatory, SQL_MANDATORY_DEL_CLASS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteClassFromMandatory, 1, &V->params.deleteClassFromMandatory.class);
    }

    if (V->stmts.deleteClassFromMandatory == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.deleteClassFromMandatory.class = class;

    rc = ExecuteStatement(V->stmts.deleteClassFromMandatory);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        return(FALSE);
    }

    PrepareStatement(V->stmts.deleteClassFromOptional, SQL_OPTIONAL_DEL_CLASS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteClassFromOptional, 1, &V->params.deleteClassFromOptional.class);
    }

    if (V->stmts.deleteClassFromOptional == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.deleteClassFromOptional.class = class;

    rc = ExecuteStatement(V->stmts.deleteClassFromOptional);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        return(FALSE);
    }

    PrepareStatement(V->stmts.deleteClassFromNaming, SQL_NAMING_DEL_CLASS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteClassFromNaming, 1, &V->params.deleteClassFromNaming.class);
    }

    if (V->stmts.deleteClassFromNaming == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.deleteClassFromNaming.class = class;

    rc = ExecuteStatement(V->stmts.deleteClassFromNaming);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        return(FALSE);
    }

    /* Ok delete the actual class now */
    PrepareStatement(V->stmts.deleteClass, SQL_DELETE_CLASS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteClass, 1, &V->params.deleteClass.class);
    }

    if (V->stmts.deleteClass== SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.deleteClass.class = class;

    rc = ExecuteStatement(V->stmts.deleteClass);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        return(FALSE);
    }

    return(TRUE);
}

BOOL 
MDBodbcListContainableClasses(const unsigned char *Object, MDBValueStruct *V)
{
    signed long id;
    SQLINTEGER rc;
    BOOL requireBindings;
    BOOL allowed;

    if (!V || !Object || !MDBodbcGetObjectID(Object, &id, NULL, &allowed, NULL, NULL, NULL, V) || !allowed) {
        return(FALSE);
    }

    PrepareStatement(V->stmts.listContainment, SQL_LIST_CONTAINMENT, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.listContainment, 1, &V->params.listContainment.object);
        BindCharResult(V->stmts.listContainment, 1, V->params.listContainment.name, MDB_ODBC_NAME_SIZE);
    }

    if (V->stmts.listContainment == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.listContainment.object = id;
    rc = ExecuteStatement(V->stmts.listContainment);
    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(V->stmts.listContainment);
    }

    while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        MDBodbcAddValue(V->params.listContainment.name, V);

        rc = SQLFetch(V->stmts.listContainment);
    }

    SQLCloseCursor(V->stmts.listContainment);

    return(V->Used);
}

const unsigned char *
MDBodbcListContainableClassesEx(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V)
{
    SQLINTEGER rc;

    if (!E || !V) {
        return(NULL);
    }

    if (E->stmt == SQL_NULL_HSTMT) {
        BOOL requireBindings;
        BOOL allowed;

        if (!Object || !MDBodbcGetObjectID(Object, &E->object, NULL, &allowed, NULL, NULL, NULL, V) || !allowed) {
            return(NULL);
        }

        /* We are not in a statement, so prepare one */
        E->sql = SQL_LIST_CONTAINMENT;
        PrepareStatement(E->stmt, SQL_LIST_CONTAINMENT, requireBindings, V);
        if (requireBindings) {
            BindLongInput(E->stmt, 1, &E->object);
            BindCharResult(E->stmt, 1, E->result, MDB_ODBC_NAME_SIZE);
        }

        rc = ExecuteStatement(E->stmt);
        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
            ReleaseStatement(V, E->stmt);
            E->stmt = SQL_NULL_HSTMT;
        }
    }

    if (E->stmt != SQL_NULL_HSTMT && E->sql == SQL_LIST_CONTAINMENT) {
        rc = SQLFetch(E->stmt);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            return(E->result);
        } else if (rc == SQL_NO_DATA) {
            ReleaseStatement(V, E->stmt);
            E->stmt = SQL_NULL_HSTMT;
        }
    }

    return(NULL);
}

long 
MDBodbcRead(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
    signed long id;
    SQLINTEGER rc;
    BOOL requireBindings;
    unsigned char *value = NULL;
    unsigned long used = 0;
    unsigned long before;
    BOOL allowed;

    if (!Object || !Attribute || !V || !MDBodbcGetObjectID(Object, &id, NULL, &allowed, NULL, NULL, NULL, V) || !allowed) {
        return(FALSE);
    }

    before = V->Used;

    PrepareStatement(V->stmts.readValue, SQL_READ_VALUE, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.readValue, 1, &V->params.readValue.object);
        BindCharInput(V->stmts.readValue, 2,
            &V->params.readValue.attribute, &V->params.readValue.attributeLength, MDB_ODBC_NAME_SIZE);

        BindCharResult(V->stmts.readValue, 1, V->params.readValue.value, MDB_ODBC_PART_SIZE + 1);
        BindLongResult(V->stmts.readValue, 2, &V->params.readValue.dn);
        BindLongResult(V->stmts.readValue, 3, &V->params.readValue.bool);
        BindLongResult(V->stmts.readValue, 4, &V->params.readValue.complete);
        BindLongResult(V->stmts.readValue, 5, &V->params.readValue.type);
    }

    if (V->stmts.readValue == SQL_NULL_HSTMT) {
        return(0);
    }

    V->params.readValue.object = id;
    StoreCharInput(Attribute, V->params.readValue.attribute, V->params.readValue.attributeLength, MDB_ODBC_NAME_SIZE);

    rc = ExecuteStatement(V->stmts.readValue);
    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(V->stmts.readValue);
    }

    while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        switch (V->params.readValue.type) {
            case MDB_ATTR_SYN_STRING: {
                if (value || V->params.readValue.complete != 1) {
                    value = realloc(value, used + MDB_ODBC_PART_SIZE + 1);

                    strncpy(value + used, V->params.readValue.value, MDB_ODBC_PART_SIZE + 1);
                    used += strlen(V->params.readValue.value);
                    value[used] = '\0';
                }

                if (V->params.readValue.complete == 1) {
                    MDBodbcAddValue(value ? value : V->params.readValue.value, V);
                    if (value) {
                        free(value);
                        value = NULL;
                        used = 0;
                    }
                }

                break;
            }

            case MDB_ATTR_SYN_DIST_NAME: {
                unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];

                /* We have a DN value */
                if (MDBodbcGetObjectDN(dn, MDB_MAX_OBJECT_CHARS, V->params.readValue.dn, FALSE, V)) {
                    MDBodbcAddValue(dn, V);
                }

                break;
            }

            case MDB_ATTR_SYN_BOOL: {
                if (V->params.readValue.bool == 1) {
                    MDBodbcAddValue("1", V);
                } else {
                    MDBodbcAddValue("0", V);
                }

                break;
            }
        }

        V->params.readValue.value[0] = '\0';
        rc = SQLFetch(V->stmts.readValue);
    }

    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        XplConsolePrintf("Error reading value: %lu\n", rc);
        PrintSQLErr(V, V->stmts.readValue);
    }

    SQLCloseCursor(V->stmts.readValue);

    if (value) {
        XplConsolePrintf("%s:%d  Failed to read the final part of a value\n", __FILE__, __LINE__);
        free(value);
    }

    return(V->Used - before);
}

const unsigned char *
MDBodbcReadEx(const unsigned char *Object, const unsigned char *Attribute, MDBEnumStruct *E, MDBValueStruct *V)
{
    SQLINTEGER rc;

    if (!E || !V) {
        return(NULL);
    }

    if (E->largeResult) {
        free(E->largeResult);
        E->largeResult = NULL;
    }

    if (E->stmt == SQL_NULL_HSTMT) {
        BOOL requireBindings;
        BOOL allowed;

        if (!Object || !Attribute || !MDBodbcGetObjectID(Object, &E->object, NULL, &allowed, NULL, NULL, NULL, V) || !allowed) {
            return(NULL);
        }

        /* We are not in a statement, so prepare one */
        E->sql = SQL_READ_VALUE;
        PrepareStatement(E->stmt, SQL_READ_VALUE, requireBindings, V);
        if (requireBindings) {
            unsigned long attributeLength = strlen(Attribute);

            BindLongInput(E->stmt, 1, &E->object);
            BindCharInput(E->stmt, 2, &Attribute, &attributeLength, MDB_ODBC_NAME_SIZE);

            BindCharResult(E->stmt, 1, E->result, MDB_ODBC_PART_SIZE + 1);
            BindLongResult(E->stmt, 2, &E->id);
            BindLongResult(E->stmt, 3, &E->bool);
            BindLongResult(E->stmt, 4, &E->complete);
            BindLongResult(E->stmt, 5, &E->type);
        }

        rc = ExecuteStatement(E->stmt);
        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
            ReleaseStatement(V, E->stmt);
            E->stmt = SQL_NULL_HSTMT;
        }
    }

    if (E->stmt != SQL_NULL_HSTMT && E->sql == SQL_READ_VALUE) {
        rc = SQLFetch(E->stmt);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            switch (V->params.readValue.type) {
                case MDB_ATTR_SYN_STRING: {
                    unsigned long used = 0;

                    do {
                        if (E->largeResult || E->complete != 1) {
                            E->largeResult = realloc(E->largeResult, used + MDB_ODBC_PART_SIZE + 1);

                            strncpy(E->largeResult + used, E->result, MDB_ODBC_PART_SIZE + 1);
                            used += strlen(E->result);
                            E->largeResult[used] = '\0';
                        }
                    } while (E->complete != 1 && (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO));

                    if (E->complete == 1) {
                        if (E->largeResult) {
                            return(E->largeResult);
                        } else {
                            return(E->result);
                        }
                    }

                    break;
                }

                case MDB_ATTR_SYN_DIST_NAME: {
                    /* We have a DN value */
                    if (MDBodbcGetObjectDN(E->result, MDB_MAX_OBJECT_CHARS, E->id, FALSE, V)) {
                        return(E->result);
                    }

                    break;
                }

                case MDB_ATTR_SYN_BOOL: {
                    if (E->bool == 1) {
                        E->result[0] = '1';
                    } else {
                        E->result[0] = '0';
                    }
                    E->result[1] = '\0';

                    return(E->result);
                }
            }
        } else if (rc == SQL_NO_DATA) {
            ReleaseStatement(V, E->stmt);
            E->stmt = SQL_NULL_HSTMT;
        }
    }

    return(NULL);
}

BOOL 
MDBodbcWriteTyped(const unsigned char *Object, const unsigned char *Attribute, const int AttrType, MDBValueStruct *V)
{
    return(MDBodbcWriteAny(Object, Attribute, AttrType, TRUE, NULL, V));
}

BOOL 
MDBodbcWrite(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
    return(MDBodbcWriteAny(Object, Attribute, MDB_ATTR_SYN_STRING, TRUE, NULL, V));
}

BOOL 
MDBodbcWriteDN(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
    return(MDBodbcWriteAny(Object, Attribute, MDB_ATTR_SYN_DIST_NAME, TRUE, NULL, V));
}

BOOL 
MDBodbcAdd(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
    if (Value) {
        return(MDBodbcWriteAny(Object, Attribute, MDB_ATTR_SYN_STRING, FALSE, Value, V));
    } else {
        /* WriteAny will attempt to write the values in V if Value is NULL */
        return(FALSE);
    }
}

BOOL 
MDBodbcAddDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
    if (Value) {
        return(MDBodbcWriteAny(Object, Attribute, MDB_ATTR_SYN_DIST_NAME, FALSE, Value, V));
    } else {
        /* WriteAny will attempt to write the values in V if Value is NULL */
        return(FALSE);
    }
}

static __inline BOOL 
MDBodbcWriteAny(const unsigned char *Object, const unsigned char *Attribute, const unsigned char Type, const BOOL clear, const unsigned char *Value, MDBValueStruct *V)
{
    SQLINTEGER object;
    SQLINTEGER attribute;
    BOOL single;
    unsigned char type;
    unsigned char **v = (Value) ? (unsigned char **)&(Value) : V->Value;
    unsigned long u = (Value) ? 1 : V->Used;
    SQLINTEGER rc;
    BOOL requireBindings;
    BOOL allowed;

    if (!Object || !Attribute || !V || !MDBodbcGetObjectID(Object, &object, NULL, NULL, &allowed, NULL, NULL, V) || !allowed) {
        return(FALSE);
    }

    PrepareStatement(V->stmts.getObjectAttrInfo, SQL_GET_OBJECT_ATTR_INFO, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.getObjectAttrInfo, 1, &V->params.getObjectAttrInfo.object);
        BindCharInput(V->stmts.getObjectAttrInfo, 2,
            V->params.getObjectAttrInfo.attribute, &V->params.getObjectAttrInfo.attributeLength, MDB_ODBC_NAME_SIZE);
        BindLongResult(V->stmts.getObjectAttrInfo, 1, &V->params.getObjectAttrInfo.id);
        BindLongResult(V->stmts.getObjectAttrInfo, 2, &V->params.getObjectAttrInfo.single);
        BindLongResult(V->stmts.getObjectAttrInfo, 3, &V->params.getObjectAttrInfo.type);
    }

    if (V->stmts.getObjectAttrInfo == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.getObjectAttrInfo.object = object;
    StoreCharInput(Attribute, V->params.getObjectAttrInfo.attribute, V->params.getObjectAttrInfo.attributeLength, MDB_ODBC_NAME_SIZE);

    /* Ok, we're all setup - Do the query */
    rc = ExecuteStatement(V->stmts.getObjectAttrInfo);
    
    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        SQLINTEGER count = 0;

        rc = SQLRowCount(V->stmts.getObjectAttrInfo, &count);
        if (count < 1) {
            /* No rows will be returned if we are not allowed to write this attribute to this object */
            return(FALSE);
        }
    }

    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(V->stmts.getObjectAttrInfo);
        SQLCloseCursor(V->stmts.getObjectAttrInfo);
    }

    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        attribute = V->params.getObjectAttrInfo.id;
        single = (V->params.getObjectAttrInfo.single == 1);
        type = (unsigned char) V->params.getObjectAttrInfo.type;
    } else {
        return(FALSE);
    }

    /*
        We now have the object ID, attribute ID, and the value for single and type.  We can now begin.
    */

    /* If they are trying to write multiple values to a single, then bail */
    if (single && u > 1) {
        return(FALSE);
    }

    /* If it is a single value attribute make sure there are no other values stored already */
    if ((clear || single) && !MDBodbcClear(Object, Attribute, V)) {
        return(FALSE);
    }

    /*
        We can finally add the value.  Write it as the type the attribute should hold, even if it does not
        match the type that the calling program is trying to write.  If they don't match the calling program
        is wrong.
    */
    switch (type) {
        case MDB_ATTR_SYN_DIST_NAME: {
            unsigned long i;

            PrepareStatement(V->stmts.addDNValue, SQL_ADD_DN_VALUE, requireBindings, V);
            if (requireBindings) {
                BindLongInput(V->stmts.addDNValue, 1, &V->params.addDNValue.owner);
                BindLongInput(V->stmts.addDNValue, 2, &V->params.addDNValue.attribute);
                BindLongInput(V->stmts.addDNValue, 3, &V->params.addDNValue.value);
            }

            if (V->stmts.addDNValue == SQL_NULL_HSTMT) {
                return(FALSE);
            }

            for (i = 0; i < u; i++) {
                SQLINTEGER referencee;

                if (MDBodbcGetObjectID(v[i], &referencee, NULL, NULL, NULL, NULL, NULL, V)) {
                    /* We have to reset these, because GetObjectID will have overwritten then */
                    V->params.addDNValue.owner = object;
                    V->params.addDNValue.attribute = attribute;
                    V->params.addDNValue.value = referencee;

                    /* Ok, we're all setup - Do the query */
                    rc = ExecuteStatement(V->stmts.addDNValue);
                    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
                        return(FALSE);
                    }

                    if (attribute == MDBodbc.config.special.member) {
                        /* This is changing group membership, which means we need to copy rights to the members */

                        if (!MDBodbcCopyObjectRights(referencee, object, FALSE, V)) {
                            return(FALSE);
                        }
                    }
                } else {
                    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
                    return(FALSE);
                }
            }

            break;
        }

        case MDB_ATTR_SYN_BOOL: {
            unsigned long i;

            PrepareStatement(V->stmts.addBoolValue, SQL_ADD_BOOL_VALUE, requireBindings, V);
            if (requireBindings) {
                BindLongInput(V->stmts.addBoolValue, 1, &V->params.addBoolValue.owner);
                BindLongInput(V->stmts.addBoolValue, 2, &V->params.addBoolValue.attribute);
                BindLongInput(V->stmts.addBoolValue, 3, &V->params.addBoolValue.value);
            }

            if (V->stmts.addBoolValue == SQL_NULL_HSTMT) {
                return(FALSE);
            }

            V->params.addBoolValue.owner = object;
            V->params.addBoolValue.attribute = attribute;

            for (i = 0; i < u; i++) {
                BOOL value;

                switch (v[i][0]) {
                    case '1':
                    case 'Y':
                    case 'y':
                    case 'T':
                    case 't': {
                        value = TRUE;
                    }

                    case '0':
                    case 'N':
                    case 'n':
                    case 'F':
                    case 'f':
                    default: {
                        value = FALSE;
                    }
                }

                /* Ok, we're all setup - Do the query */
                rc = ExecuteStatement(V->stmts.addBoolValue);
                if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
                    return(FALSE);
                }
            }

            break;
        }

        case MDB_ATTR_SYN_BINARY: {
            /*
                FIXME - This should be implemented by Base 64ing the data, and storing it as if
                it where a string.  It would be best if it can be base 64'ed into the param that
                is already bound so that we don't have to worry about memory.
            */
            break;
        }

        case MDB_ATTR_SYN_STRING: {
            unsigned long i;

            PrepareStatement(V->stmts.addStringValue, SQL_ADD_STRING_VALUE, requireBindings, V);
            if (requireBindings) {
                BindLongInput(V->stmts.addStringValue, 1, &V->params.addStringValue.owner);
                BindLongInput(V->stmts.addStringValue, 2, &V->params.addStringValue.attribute);
                BindCharInput(V->stmts.addStringValue, 3,
                    V->params.addStringValue.value, &V->params.addStringValue.valueLength, MDB_ODBC_PART_SIZE);;
                BindLongInput(V->stmts.addStringValue, 4, &V->params.addStringValue.complete);
            }

            if (V->stmts.addStringValue == SQL_NULL_HSTMT) {
                return(FALSE);
            }

            V->params.addStringValue.owner = object;
            V->params.addStringValue.attribute = attribute;

            for (i = 0; i < u; i++) {
                unsigned char *value = v[i];

                /*
                    It is important to be able to store strings of any length as a value, but it is much easier to deal with
                    the database if we have fixed sized records.  Also some ODBC drivers may not like variable sized records.

                    To handle this any string value larger than MDBC_ODBC_PART_SIZE (255) will be split into multiple records
                    which will be stored in order.  The complete row will be set to true on the final record, to allow for easy
                    reading.  Simply read them order by id until complete is true.

                    That means they have to be split here though.  This should rarely actually happen, because most values will 
                    be much smaller than 255.
                */

                do {
                    StoreCharInput(value, V->params.addStringValue.value, V->params.addStringValue.valueLength, MDB_ODBC_PART_SIZE);
                    V->params.addStringValue.complete = (V->params.addStringValue.valueLength != MDB_ODBC_PART_SIZE);

                    /* Ok, we're all setup - Do the query */
                    rc = ExecuteStatement(V->stmts.addStringValue);
                    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
                        return(FALSE);
                    }

                    value += MDB_ODBC_PART_SIZE;
                } while (V->params.addStringValue.valueLength == MDB_ODBC_PART_SIZE);
            }

            break;
        }

        default: {
            return(FALSE);
        }
    }

    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
    return(TRUE);
}

BOOL 
MDBodbcRemove(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
    MDBValueStruct *v;
    unsigned long i;

    if (!Object || !Attribute || !Value || !V) {
        return(FALSE);
    }

    v = MDBodbcShareContext(V);

    MDBodbcRead(Object, Attribute, v);
    for (i = 0; i < v->Used; i++) {
        if (XplStrCaseCmp(v->Value[i], Value)) {
            MDBodbcFreeValue(i, v);
            MDBodbcWrite(Object, Attribute, v);
            MDBodbcDestroyValueStruct(v);

            return(TRUE);
        }
    }

    V->ErrNo = ERR_NO_SUCH_VALUE;

    MDBodbcDestroyValueStruct(v);
    return(FALSE);
}

BOOL 
MDBodbcRemoveDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
    MDBValueStruct *v;
    unsigned long i;
    SQLINTEGER value;

    if (!Object || !Attribute || !Value || !V || !MDBodbcGetObjectID(Value, &value, NULL, NULL, NULL, NULL, NULL, V)) {
        return(FALSE);
    }

    v = MDBodbcShareContext(V);

    MDBodbcRead(Object, Attribute, v);
    for (i = 0; i < v->Used; i++) {
        SQLINTEGER c;

        if (MDBodbcGetObjectID(v->Value[i], &c, NULL, NULL, NULL, NULL, NULL, V) && c == value) {
            MDBodbcFreeValue(i, v);
            MDBodbcWriteDN(Object, Attribute, v);
            MDBodbcDestroyValueStruct(v);
            return(TRUE);
        }
    }

    V->ErrNo = ERR_NO_SUCH_VALUE;

    MDBodbcDestroyValueStruct(v);
    return(FALSE);
}

BOOL 
MDBodbcClear(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
    SQLINTEGER id;
    BOOL allow;

    if (Object && Attribute && V && MDBodbcGetObjectID(Object, &id, NULL, NULL, &allow, NULL, NULL, V) && allow) {
        SQLINTEGER attribute = 0;
        SQLINTEGER rc;
        BOOL requireBindings;

        PrepareStatement(V->stmts.getAttrByName, SQL_GET_ATTRIBUTE_BY_NAME, requireBindings, V);
        if (requireBindings) {
            BindCharInput(V->stmts.getAttrByName, 1,
                V->params.getAttrByName.attribute, &V->params.getAttrByName.attributeLength, MDB_ODBC_NAME_SIZE);
            BindLongResult(V->stmts.getAttrByName, 1, &V->params.getAttrByName.id);
        }

        if (V->stmts.getAttrByName == SQL_NULL_HSTMT) {
            return(FALSE);
        }

        StoreCharInput(Attribute, V->params.getAttrByName.attribute, V->params.getAttrByName.attributeLength, MDB_ODBC_NAME_SIZE);

        /* Ok, we're all setup - Do the query */
        rc = ExecuteStatement(V->stmts.getAttrByName);

        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(V->stmts.getAttrByName);
            SQLCloseCursor(V->stmts.getAttrByName);
        }

        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            attribute = V->params.getAttrByName.id;
        } else {
            return(FALSE);
        }

        if (attribute == MDBodbc.config.special.member) {
            /* The attribute is 'Member' so we need to remove the rights that where granted by this group to its members. */
            PrepareStatement(V->stmts.delRightsByProvider, SQL_DEL_RIGHTS_BY_PROVIDER, requireBindings, V);
            if (requireBindings) {
                BindLongInput(V->stmts.delRightsByProvider, 1, &V->params.delRightsByProvider.provider);
            }

            if (V->stmts.delRightsByProvider == SQL_NULL_HSTMT) {
                return(FALSE);
            }

            V->params.delRightsByProvider.provider = id;

            /* Ok, we're all setup - Do the query */
            rc = ExecuteStatement(V->stmts.delRightsByProvider);
            if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
                return(FALSE);
            }
        }

        /* Ok try to delete the values now */
        PrepareStatement(V->stmts.clearAttribute, SQL_CLEAR_ATTRIBUTE, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.clearAttribute, 1, &V->params.clearAttribute.object);
            BindLongInput(V->stmts.clearAttribute, 2, &V->params.clearAttribute.attribute);
        }

        if (V->stmts.clearAttribute == SQL_NULL_HSTMT) {
            return(FALSE);
        }

        V->params.clearAttribute.object = id;
        V->params.clearAttribute.attribute = attribute;

        /* Ok, we're all setup - Do the query */
        rc = ExecuteStatement(V->stmts.clearAttribute);

        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO || rc == SQL_NO_DATA) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
            return(TRUE);
        } else {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
            return(FALSE);
        }
    }

    return(FALSE);
}

BOOL 
MDBodbcIsObject(const unsigned char *Object, MDBValueStruct *V)
{
    signed long id;
    BOOL allow;

    return(Object && V && MDBodbcGetObjectID(Object, &id, NULL, &allow, NULL, NULL, NULL, V) && allow);
}

BOOL 
MDBodbcGetObjectDetails(const unsigned char *Object, unsigned char *Type, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V)
{
    signed long id;
    BOOL allow;

    if (!Object || !V || !MDBodbcGetObjectID(Object, &id, NULL, &allow, NULL, NULL, NULL, V) || !allow) {
        return(FALSE);
    }

    if (RDN && !MDBodbcGetObjectDN(RDN, MDB_MAX_OBJECT_CHARS, id, TRUE, V)) {
        return(FALSE);
    }

    if (DN) {
        unsigned long base = *(V->base.id);

        *(V->base.id) = -1;
        if (!MDBodbcGetObjectDN(DN, MDB_MAX_OBJECT_CHARS, id, FALSE, V)) {
            *(V->base.id) = base;
            return(FALSE);
        }

        *(V->base.id) = base;
    }

    if (Type) {
        SQLINTEGER rc;
        BOOL requireBindings;

        PrepareStatement(V->stmts.getObjectClassName, SQL_GET_OBJECT_CLASSNAME, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.getObjectClassName, 1, &V->params.getObjectClassName.object);
            BindCharResult(V->stmts.getObjectClassName, 1, V->params.getObjectClassName.class, MDB_ODBC_NAME_SIZE);
        }

        if (V->stmts.getObjectClassName == SQL_NULL_HSTMT) {
            return(FALSE);
        }

        V->params.getObjectClassName.object = id;

        /* Ok, do it */
        rc = ExecuteStatement(V->stmts.getObjectClassName);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(V->stmts.getObjectClassName);
            SQLCloseCursor(V->stmts.getObjectClassName);
        }

        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            strcpy(Type, V->params.getObjectClassName.class);
        } else {
            return(FALSE);
        }
    }

    return(TRUE);
}

BOOL 
MDBodbcGetObjectDetailsEx(const unsigned char *Object, MDBValueStruct *Types, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V)
{
    /* FIXME: Implement this function */
    XplConsolePrintf("MDBodbcGetObjectDetailsEx() not implemented!\n");
    return FALSE;
}

BOOL 
MDBodbcVerifyPassword(const unsigned char *Object, const unsigned char *Password, MDBValueStruct *V)
{
    signed long id;
    MD5_CTX md5;
    unsigned char hash[32 + 1];
    unsigned char digest[16];
    unsigned long i;
    SQLINTEGER rc;
    BOOL requireBindings;

    if (!Object || !Password || !V || !MDBodbcGetObjectID(Object, &id, NULL, NULL, NULL, NULL, NULL, V)) {
        return(FALSE);
    }

    MD5_Init(&md5);
    MD5_Update(&md5, Password, strlen(Password));
    MD5_Final(digest, &md5);

    for (i = 0; i < 16; i++) {
        sprintf(hash + (i * 2), "%02x", digest[i]);
    }

    PrepareStatement(V->stmts.verifyPassword, SQL_VERIFY_PASSWORD, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.verifyPassword, 1, &V->params.verifyPassword.object);
        BindCharInput(V->stmts.verifyPassword, 2,
            &V->params.verifyPassword.pass, &V->params.verifyPassword.passLength, 32);
    }

    if (V->stmts.verifyPassword != SQL_NULL_HSTMT) {
        V->params.verifyPassword.object = id;
        StoreCharInput(hash, V->params.verifyPassword.pass, V->params.verifyPassword.passLength, 32);

        /* Ok, we're ready */
        rc = ExecuteStatement(V->stmts.verifyPassword);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(V->stmts.verifyPassword);
            SQLCloseCursor(V->stmts.verifyPassword);

            if (((rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) &&
                V->params.verifyPassword.object == id) ||
                (rc == SQL_NO_DATA && Password[0] == '\0'))
            {
                return(TRUE);
            }
        }
    }

    return(FALSE);
}

BOOL 
MDBodbcChangePassword(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V)
{
    if (!OldPassword) {
        return(FALSE);
    }

    return(MDBodbcChangePasswordEx(Object, OldPassword, NewPassword, V));
}

BOOL 
MDBodbcChangePasswordEx(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V)
{
    signed long id;
    MD5_CTX md5;
    unsigned char hash[32 + 1];
    unsigned char digest[16];
    unsigned long i;
    SQLINTEGER rc;
    BOOL requireBindings;
    BOOL allow;

    if (!Object || !NewPassword || !V || !MDBodbcGetObjectID(Object, &id, NULL, NULL, &allow, NULL, NULL, V)) {
        return(FALSE);
    }

    /*
        The password can only be changed if the current password is given, or the object logged in has
        the right to write to the object.
    */
    if ((allow && !OldPassword) || MDBodbcVerifyPassword(Object, OldPassword, V)) {
        MD5_Init(&md5);
        MD5_Update(&md5, NewPassword, strlen(NewPassword));
        MD5_Final(digest, &md5);

        for (i = 0; i < 16; i++) {
            sprintf(hash + (i * 2), "%02x", digest[i]);
        }

        /* Ok, set the new password */
        PrepareStatement(V->stmts.setPassword, SQL_SET_PASSWORD, requireBindings, V);
        if (requireBindings) {
            BindCharInput(V->stmts.setPassword, 1,
                &V->params.setPassword.pass, &V->params.setPassword.passLength, 32);
            BindLongInput(V->stmts.setPassword, 2, &V->params.setPassword.object);
        }

        if (V->stmts.setPassword == SQL_NULL_HSTMT) {
            return(FALSE);
        }

        V->params.setPassword.object = id;
        StoreCharInput(hash, V->params.setPassword.pass, V->params.setPassword.passLength, 32);

        /* Ok, we're ready */
        rc = ExecuteStatement(V->stmts.setPassword);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
            return(TRUE);
        } else {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        }
    }

    return(FALSE);
}

static __inline BOOL
checkFilter(unsigned char *value, const unsigned char *filter)
{
    if (filter) {
        unsigned char *filt = strdup(filter);
        unsigned char *f = filt;

        while (f && *f) {
            switch (*f) {
                case '?': {
                    /* A question mark matches one character. */
                    if (*value == '\0') { 
                        free(filt);
                        return(FALSE);
                    }

                    value++;
                    f++;

                    break;  
                }

                case '*': {
                    unsigned char *ptr;
                    unsigned char *e;
                    unsigned char w = '\0'; 

                    /* An asterisk matches zero or more characters. */

                    do {
                        f++;
                    } while (*f == '*');

                    if (*f == '\0') { 
                        /* The asterisk is the last char, so the string matches */
                        free(filt);
                        return(TRUE);
                    }

                    /* Find the next wildcard */
                    e = (unsigned char *) f; 
                    while (*e && *e != '*' && *e != '?') {
                        e++;
                    }

                    w = *e; 
                    *e = '\0'; 

                    ptr = strstr(value, f);
                    if (!ptr) {
                        free(filt);
                        return(FALSE);
                    }

                    value = ptr + strlen(f);
                    f = e;

                    *e = w; 
                    break;  
                }

                default: {
                    /* All other characters match themselves. */
                    if (toupper(*f) != toupper(*value)) {
                        free(filt);
                        return(FALSE);
                    }

                    f++;
                    value++;

                    break;  
                }
            }
        }

        free(filt);
        return(*value == '\0');
    } else {
        /* If there is no filter, then it is a match */
        return(TRUE);
    }
}

long
MDBodbcEnumerateObjects(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, MDBValueStruct *V)
{
    signed long id;
    SQLINTEGER rc;
    BOOL requireBindings;
    SQLHSTMT getChildren;
    unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char *e;
    BOOL allow;

    if (!Container || !V || !MDBodbcGetObjectID(Container, &id, NULL, &allow, NULL, NULL, NULL, V) ||
        !allow || !MDBodbcGetObjectDN(dn, MDB_MAX_OBJECT_CHARS, id, TRUE, V))
    {
        return(FALSE);
    }

    /* Set the position to write the new object names to */
    e = dn + strlen(dn);
    *e = '\\';
    e++;

    /* Prepare both the statement with the class and without, bind to the same params */
    PrepareStatement(V->stmts.getChildren, SQL_GET_CHILDREN, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.getChildren, 1, &V->params.getChildren.object);
        BindCharResult(V->stmts.getChildren, 1, V->params.getChildren.child, MDB_ODBC_PART_SIZE + 1);
        BindLongResult(V->stmts.getChildren, 2, &V->params.getChildren.container);
    }

    PrepareStatement(V->stmts.getChildrenByClass, SQL_GET_CHILDREN_BY_CLASS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.getChildrenByClass, 1, &V->params.getChildren.object);
        BindCharInput(V->stmts.getChildrenByClass, 2,
            &V->params.getChildren.class, &V->params.getChildren.classLength, MDB_ODBC_NAME_SIZE);
        BindLongInput(V->stmts.getChildrenByClass, 3, &V->params.getChildren.allContainers);
        BindCharResult(V->stmts.getChildrenByClass, 1, V->params.getChildren.child, MDB_ODBC_PART_SIZE + 1);
        BindLongResult(V->stmts.getChildrenByClass, 2, &V->params.getChildren.container);
    }

    if (V->stmts.getChildren == SQL_NULL_HSTMT || V->stmts.getChildrenByClass == SQL_NULL_HSTMT) {
        return(0);
    }

    /* All Containers should be set to -1 for normal use */
    V->params.getChildren.allContainers = -1;
    V->params.getChildren.object = id;

    if (Type && Type[0] != '\0' && !(Type[0] == '*' && Type[1] == '\0')) {
        StoreCharInput(Type, V->params.getChildren.class, V->params.getChildren.classLength, MDB_ODBC_NAME_SIZE);
        getChildren = V->stmts.getChildrenByClass;
    } else {
        getChildren = V->stmts.getChildren;
    }

    rc = ExecuteStatement(getChildren);

    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(getChildren);
    }

    while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        if (!Pattern || !(*Pattern) || checkFilter(V->params.getChildren.child, Pattern)) {
            strncpy(e, V->params.getChildren.child, MDB_MAX_OBJECT_CHARS - (e - dn));
            MDBodbcAddValue(dn, V);
        }

        rc = SQLFetch(getChildren);
    }

    SQLCloseCursor(getChildren);

    return(V->Used);
}

const unsigned char *
MDBodbcEnumerateObjectsEx(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, unsigned long Flags, MDBEnumStruct *E, MDBValueStruct *V)
{
    SQLINTEGER rc;

    if (!E || !V) {
        return(NULL);
    }

    if (E->stmt == SQL_NULL_HSTMT) {
        BOOL requireBindings;
        BOOL allow;

        if (!Container || !MDBodbcGetObjectID(Container, &E->object, NULL, &allow, NULL, NULL, NULL, V) ||
            !allow || !MDBodbcGetObjectDN(E->dn, MDB_MAX_OBJECT_CHARS, E->object, FALSE, V))
        {
            return(NULL);
        }

        E->name = E->dn + strlen(E->dn);
        *(E->name) = '\\';
        (E->name)++;

        /* We are not in a statement, so prepare one */
        if (Type && Type[0] != '\0' && !(Type[0] == '*' && Type[1] == '\0')) {
            E->classlen = (Type) ? strlen(Type) : 0;
            strcpy(E->class, Type);

            E->sql = SQL_GET_CHILDREN_BY_CLASS;

            if (Flags) {
                E->allContainers = 1;
            } else {
                E->allContainers = -1;
            }
        } else {
            E->sql = SQL_GET_CHILDREN;
        }

        PrepareStatement(E->stmt, E->sql, requireBindings, V);
        if (requireBindings) {
            BindLongInput(E->stmt, 1, &E->object);

            if (E->sql == SQL_GET_CHILDREN_BY_CLASS) {
                BindCharInput(E->stmt, 2, &E->class, &E->classlen, MDB_ODBC_NAME_SIZE);
                BindLongInput(E->stmt, 3, &E->allContainers);
            }

            BindCharResult(E->stmt, 1, E->result, MDB_ODBC_NAME_SIZE);
            BindLongResult(E->stmt, 2, &E->container);
        }

        rc = ExecuteStatement(E->stmt);
        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
            ReleaseStatement(V, E->stmt);
            E->stmt = SQL_NULL_HSTMT;
        }
    }

    if (E->stmt != SQL_NULL_HSTMT && (E->sql == SQL_GET_CHILDREN || E->sql == SQL_GET_CHILDREN_BY_CLASS)) {
        rc = SQLFetch(E->stmt);
        while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            if ((Flags && E->container) || !Pattern || !(*Pattern) || checkFilter(E->result, Pattern)) {
                strncpy(E->name, E->result, MDB_MAX_OBJECT_CHARS - (E->name - E->dn));
                return(E->dn);
            }

            rc = SQLFetch(E->stmt);
        }

        if (rc == SQL_NO_DATA) {
            ReleaseStatement(V, E->stmt);
            E->stmt = SQL_NULL_HSTMT;
        }
    }

    return(NULL);
}

const unsigned char *
MDBodbcEnumerateAttributesEx(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V)
{
    return(FALSE);
}

BOOL 
MDBodbcCreateAlias(const unsigned char *Alias, const unsigned char *AliasedObjectDn, MDBValueStruct *V)
{
    return(FALSE);
}

BOOL 
MDBodbcCreateObject(const unsigned char *Object, const unsigned char *Class, MDBValueStruct *Attribute, MDBValueStruct *Data, MDBValueStruct *V)
{
    SQLINTEGER parent;
    SQLINTEGER parentClass;
    SQLINTEGER id;
    unsigned char *ptr;
    unsigned char buffer[MDB_MAX_OBJECT_CHARS + 1];
    unsigned long i;
    SQLINTEGER rc;
    BOOL requireBindings;
    BOOL allow;

    strncpy(buffer, Object, MDB_MAX_OBJECT_CHARS);
    ptr = strrchr(buffer, '\\');

    if (ptr == buffer) {
        if (!MDBodbcGetObjectID("\\", &parent, &parentClass, NULL, &allow, NULL, NULL, V)) {
            return(FALSE);
        }

        ptr++;
    } else if (ptr) {
        *ptr = '\0';
        if (!MDBodbcGetObjectID(buffer, &parent, &parentClass, NULL, &allow, NULL, NULL, V)) {
            return(FALSE);
        }

        ptr++;
    } else {
        parent = *(V->base.id);
        parentClass = *(V->base.class);
        ptr = buffer;

        allow = *(V->base.write);
    }

    if (!allow) {
        return(FALSE);
    }

    /* Verify mandatory attributes */
    PrepareStatement(V->stmts.getMandatoryByName, SQL_GET_MANDATORY_BY_NAME, requireBindings, V);
    if (requireBindings) {
        BindCharInput(V->stmts.getMandatoryByName, 1,
            &V->params.getMandatoryByName.class, &V->params.getMandatoryByName.classLength, MDB_ODBC_NAME_SIZE);
        BindCharResult(V->stmts.getMandatoryByName, 1, V->params.getMandatoryByName.class, MDB_ODBC_NAME_SIZE);
    }

    if (V->stmts.getMandatoryByName != SQL_NULL_HSTMT) {
        StoreCharInput(Class, V->params.getMandatoryByName.class, V->params.getMandatoryByName.classLength, MDB_ODBC_NAME_SIZE);

        rc = ExecuteStatement(V->stmts.getMandatoryByName);
        if ((rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) && (!Attribute || Attribute->Used == 0)) {
            SQLINTEGER count = 0;

            /* We don't have any attributes, so if we have any mandatorys then we fail right now */
            rc = SQLRowCount(V->stmts.getMandatoryByName, &count);
            SQLCloseCursor(V->stmts.getMandatoryByName);

            if (count > 0) {
                V->ErrNo = ERR_MISSING_MANDATORY;
                return(FALSE);
            }
        } else {
            if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                rc = SQLFetch(V->stmts.getMandatoryByName);
            }

            while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                unsigned long i;
                BOOL found = FALSE;

                for (i = 0; i < Attribute->Used; i++) {
                    /* Ignore the first char */
                    if (XplStrCaseCmp(Attribute->Value[i] + 1, V->params.getMandatoryByName.class) == 0) {
                        found = TRUE;
                        break;
                    }
                }

                if (!found) {
                    SQLCloseCursor(V->stmts.getMandatoryByName);
                    V->ErrNo = ERR_MISSING_MANDATORY;
                    return(FALSE);
                }

                rc = SQLFetch(V->stmts.getMandatoryByName);
            }

            if (rc != SQL_NO_DATA) {
                V->ErrNo = ERR_MISSING_MANDATORY;
                return(FALSE);
            } else {
                SQLCloseCursor(V->stmts.getMandatoryByName);
            }
        }
    }

    PrepareStatement(V->stmts.createObject, SQL_CREATE_OBJECT, requireBindings, V);
    if (requireBindings) {
        BindCharInput(V->stmts.createObject, 1,
            &V->params.createObject.name, &V->params.createObject.nameLength, MDB_ODBC_NAME_SIZE);
        BindLongInput(V->stmts.createObject, 2, &V->params.createObject.parent);
        BindCharInput(V->stmts.createObject, 3,
            &V->params.createObject.class, &V->params.createObject.classLength, MDB_ODBC_NAME_SIZE);
        BindLongInput(V->stmts.createObject, 4, &V->params.createObject.parentClass);
    }

    if (V->stmts.createObject != SQL_NULL_HSTMT) {
        V->params.createObject.parent = parent;
        V->params.createObject.parentClass = parentClass;
        StoreCharInput(ptr, V->params.createObject.name, V->params.createObject.nameLength, MDB_ODBC_NAME_SIZE);
        StoreCharInput(Class, V->params.createObject.class, V->params.createObject.classLength, MDB_ODBC_NAME_SIZE);

        /* Ok, we're ready */
        rc = ExecuteStatement(V->stmts.createObject);
        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
            return(FALSE);
        }
    }

    /*
        Setup rights

        Get the ID of the newly created object, and then copy the rights of the parent object
        to it, with parent set properly.
    */
    if (!MDBodbcGetObjectID(Object, &id, NULL, NULL, NULL, NULL, NULL, V)) {
        /*
            The object doesn't seem to have actually be created.  This implies it can not be
            contained in this type of container.
        */
        V->ErrNo = ERR_ILLEGAL_CONTAINMENT;
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    if (!MDBodbcCopyObjectRights(id, parent, TRUE, V)) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    if (Attribute && Data) {
        unsigned long l = Attribute->Used;

        if (l > Data->Used) {
            l = Data->Used;
        }

        for (i = 0; i < l; i++) {
            MDBodbcWriteAny(Object, Attribute->Value[i] + 1, Attribute->Value[i][0], FALSE, Data->Value[i], V);
        }
    }

    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
    return(TRUE);
}

BOOL 
MDBodbcDeleteObject(const unsigned char *Object, BOOL Recursive, MDBValueStruct *V)
{
    signed long id;
    BOOL allow;
    BOOL result;

    if (!Object || !V || !MDBodbcGetObjectID(Object, &id, NULL, NULL, NULL, &allow, NULL, V) || !allow) {
        return(FALSE);
    }

    result = MDBodbcDeleteObjectByID(id, Recursive, V);
    if (result) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
        return(TRUE);
    } else {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }
}

static __inline BOOL 
MDBodbcDeleteObjectByID(signed long id, BOOL Recursive, MDBValueStruct *V)
{
    SQLINTEGER rc;
    BOOL requireBindings;

    if (id == -1) {
        /* We don't allow deleting the root */
        return(FALSE);
    }

    /* Remove all rights associated with this object */
    PrepareStatement(V->stmts.deleteObjectRights, SQL_DELETE_OBJECT_RIGHTS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteObjectRights, 1, &V->params.deleteObjectRights.object);
    }

    if (V->stmts.deleteObjectRights == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.deleteObjectRights.object = id;
    rc = ExecuteStatement(V->stmts.deleteObjectRights);

    if (Recursive) {
        SQLHSTMT getChildrenIDs = SQL_NULL_HSTMT;
        signed long child;

        PrepareStatement(getChildrenIDs, SQL_GET_CHILDREN_IDS, requireBindings, V);
        if (requireBindings) {
            BindLongInput(getChildrenIDs, 1, &id);
            BindLongResult(getChildrenIDs, 1, &child);
        }

        if (getChildrenIDs == SQL_NULL_HSTMT) {
            return(FALSE);
        }

        rc = ExecuteStatement(getChildrenIDs);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(getChildrenIDs);
        }

        while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            if (!MDBodbcDeleteObjectByID(child, Recursive, V)) {
                ReleaseStatement(V, getChildrenIDs);
                return(FALSE);
            }

            rc = SQLFetch(getChildrenIDs);
        }

        ReleaseStatement(V, getChildrenIDs);
        if (rc != SQL_NO_DATA) {
            return(FALSE);
        }
    } else {
        SQLINTEGER count = 0;

        /* Fail if there are any children */
        PrepareStatement(V->stmts.hasChildren, SQL_HAS_CHILDREN, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.hasChildren, 1, &V->params.hasChildren.object);
            BindLongResult(V->stmts.hasChildren, 1, &V->params.hasChildren.child);
        }

        if (V->stmts.hasChildren == SQL_NULL_HSTMT) {
            return(FALSE);
        }

        V->params.hasChildren.object = id;
        rc = ExecuteStatement(V->stmts.hasChildren);
        SQLRowCount(V->stmts.hasChildren, &count);
        SQLCloseCursor(V->stmts.hasChildren);

        if (count > 0) {
            return(FALSE);
        }
    }

    /* Ok delete it now */
    PrepareStatement(V->stmts.deleteObjectValues, SQL_DELETE_OBJECT_VALUES, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteObjectValues, 1, &V->params.deleteObjectValues.object);
    }

    if (V->stmts.deleteObjectValues == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.deleteObjectValues.object = id;
    rc = ExecuteStatement(V->stmts.deleteObjectValues);

    PrepareStatement(V->stmts.deleteObject, SQL_DELETE_OBJECT, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.deleteObject, 1, &V->params.deleteObject.object);
    }

    if (V->stmts.deleteObject== SQL_NULL_HSTMT) {
        return(FALSE);
    }

    V->params.deleteObject.object = id;
    rc = ExecuteStatement(V->stmts.deleteObject);
    return(rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO);
}


BOOL 
MDBodbcRenameObject(const unsigned char *ObjectOld, const unsigned char *ObjectNew, MDBValueStruct *V)
{
    signed long object;
    signed long id;
    unsigned char *new;
    unsigned char dn[MDB_MAX_OBJECT_CHARS + 1];
    SQLINTEGER rc;
    BOOL requireBindings;
    BOOL allow;
    SQLINTEGER parent = -1;

    /* Verify that we have good arguments, and that the old object exists and that the new doesn't */
    if (!V || !ObjectNew || !ObjectOld ||
        !MDBodbcGetObjectID(ObjectOld, &object, NULL, NULL, NULL, NULL, &allow, V) || !allow)
    {
        return(FALSE);
    }

    new = strrchr(ObjectNew, '\\');
    if (new) {
        signed long class;

        strncpy(dn, ObjectNew, sizeof(dn));

        /* A path was given, not just a name.  Make sure the object referenced doesn't exist */
        if (MDBodbcGetObjectID(ObjectNew, &id, NULL, NULL, NULL, NULL, NULL, V)) {
            return(FALSE);
        }

        /* We have a container, so we need to move the object */
        dn[new - ObjectNew] = '\0';
        if (!MDBodbcGetObjectID(dn, &id, NULL, NULL, &allow, NULL, NULL, V) || !allow) {
            /* The new container doesn't exist, or we aren't allowed to create in it */
            return(FALSE);
        }

        /* Get the class of the object so we can verify containment */
        PrepareStatement(V->stmts.getObjectClass, SQL_GET_OBJECT_CLASS, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.getObjectClass, 1, &V->params.getObjectClass.object);
            BindLongResult(V->stmts.getObjectClass, 1, &V->params.getObjectClass.class);
        }

        if (V->stmts.getObjectClass == SQL_NULL_HSTMT) {
            return(FALSE);
        }

        V->params.getObjectClass.object = object;

        /* Ok, do it */
        rc = ExecuteStatement(V->stmts.getObjectClass);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(V->stmts.getObjectClass);
            SQLCloseCursor(V->stmts.getObjectClass);
        }

        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            class = V->params.getObjectClass.class;
        } else {
            return(FALSE);
        }

        /* Ok, now verify that the new container can contain this class */
        PrepareStatement(V->stmts.verifyContainment, SQL_VERIFY_CONTAINMENT, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.verifyContainment, 1, &V->params.verifyContainment.object);
            BindLongInput(V->stmts.verifyContainment, 2, &V->params.verifyContainment.class);
            BindLongResult(V->stmts.verifyContainment, 1, &V->params.verifyContainment.class);
        }

        if (V->stmts.verifyContainment == SQL_NULL_HSTMT) {
            return(FALSE);
        }

        V->params.verifyContainment.object = id;
        V->params.verifyContainment.class = class;

        /* Ok, do it */
        rc = ExecuteStatement(V->stmts.verifyContainment);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(V->stmts.verifyContainment);
            SQLCloseCursor(V->stmts.verifyContainment);
        }

        if ((rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) ||
            class != V->params.verifyContainment.class)
        {
            return(FALSE);
        }

        /*
            Verify that the new parent is not actual below the object we are trying to
            move, because that would cause a detached part of the tree.
        */
        PrepareStatement(V->stmts.getParentInfo, SQL_GET_PARENT_INFO, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.getParentInfo, 1, &V->params.getParentInfo.object);
            BindLongResult(V->stmts.getParentInfo, 1, &V->params.getParentInfo.object);
            BindCharResult(V->stmts.getParentInfo, 2, V->params.getParentInfo.name, MDB_ODBC_NAME_SIZE);
        }

        if (V->stmts.getParentInfo == SQL_NULL_HSTMT) {
            return(FALSE);
        }

        V->params.getParentInfo.object = id;

        do {
            /* Ok, do it */
            rc = ExecuteStatement(V->stmts.getParentInfo);
            if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                if (parent == -1) {
                    parent = V->params.getParentInfo.object;
                }

                rc = SQLFetch(V->stmts.getParentInfo);
                SQLCloseCursor(V->stmts.getParentInfo);
            }

            if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                break;
            }
        } while (V->params.getParentInfo.object != -1 && V->params.getParentInfo.object != object);

        if (V->params.getParentInfo.object == object) {
            /* We can't move an object to a container below it */
            return(FALSE);
        }

        /*
            Now that we know that the object can be moved to the new container, we need to
            remove all rights from this object, and all child objects that this object gets
            from its parent container.

            We can not remove rights that are provided at a lower level though.  Only remove
            rights from the children that the current object has.
        */
        if (!MDBodbcRemoveInheritedRights(id, TRUE, -1, V)) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
            return(FALSE);
        }

        /* Ok, it has now passed all the checks, so the object can be moved */
        PrepareStatement(V->stmts.moveObject, SQL_MOVE_OBJECT, requireBindings, V);
        if (requireBindings) {
            BindLongInput(V->stmts.moveObject, 1, &V->params.moveObject.parent);
            BindLongInput(V->stmts.moveObject, 2, &V->params.moveObject.object);
        }

        if (V->stmts.moveObject == SQL_NULL_HSTMT) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
            return(FALSE);
        }

        V->params.moveObject.object = object;
        V->params.moveObject.parent = id;

        /* Ok, do it */
        rc = ExecuteStatement(V->stmts.moveObject);
        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
            return(FALSE);
        }

        /* Update the pointer in case the object still needs to be renamed in its new container */
        new++;

        /* Copy the rights from the new container to the object */
        if (!MDBodbcCopyObjectRights(id, object, TRUE, V)) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
            return(FALSE);
        }
    } else {
        /* Verify that an object doesn't exist with this name already */
        strncpy(dn, ObjectOld, sizeof(dn));

        new = strrchr(dn, '\\');
        if (new) {
            *(++new) = '\0';
        }

        strncpy(new, ObjectNew, sizeof(dn) - (new - dn));
        if (MDBodbcGetObjectID(dn, &id, NULL, NULL, NULL, NULL, NULL, V)) {
            return(FALSE);
        }

        /* No object exists with that name */
        new = (unsigned char *)ObjectNew;
    }

    if (*new) {
        /* Ok, time to rename */
        PrepareStatement(V->stmts.renameObject, SQL_RENAME_OBJECT, requireBindings, V);
        if (requireBindings) {
            BindCharInput(V->stmts.renameObject, 1,
                &V->params.renameObject.name, &V->params.renameObject.nameLength, MDB_ODBC_NAME_SIZE);
            BindLongInput(V->stmts.renameObject, 2, &V->params.renameObject.object);
        }

        if (V->stmts.renameObject == SQL_NULL_HSTMT) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
            return(FALSE);
        }

        StoreCharInput(new, V->params.renameObject.name, V->params.renameObject.nameLength, MDB_ODBC_NAME_SIZE);
        V->params.renameObject.object = object;

        /* Ok, do it */
        rc = ExecuteStatement(V->stmts.renameObject);
        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
            return(FALSE);
        }
    }

    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
    return(TRUE);
}

static __inline BOOL
MDBodbcSetObjectRightsByID(SQLINTEGER trustee, SQLINTEGER object, BOOL fromParent, SQLINTEGER provider, BOOL Read, BOOL Write, BOOL Delete, BOOL Rename, MDBValueStruct *V)
{
    SQLHSTMT getChildrenIDs = SQL_NULL_HSTMT;
    SQLINTEGER child;
    SQLINTEGER rc;
    BOOL requireBindings;

    PrepareStatement(V->stmts.setRights, SQL_SET_RIGHTS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.setRights, 1, &V->params.setGetRights.object);
        BindLongInput(V->stmts.setRights, 2, &V->params.setGetRights.trustee);
        BindLongInput(V->stmts.setRights, 3, &V->params.setGetRights.provider);
        BindLongInput(V->stmts.setRights, 4, &V->params.setGetRights.parent);
        BindLongInput(V->stmts.setRights, 5, &V->params.setGetRights.read);
        BindLongInput(V->stmts.setRights, 6, &V->params.setGetRights.write);
        BindLongInput(V->stmts.setRights, 7, &V->params.setGetRights.delete);
        BindLongInput(V->stmts.setRights, 8, &V->params.setGetRights.rename);
    }

    V->params.setGetRights.object = object;
    V->params.setGetRights.trustee = trustee;

    V->params.setGetRights.parent = fromParent ? 1 : 0;
    V->params.setGetRights.provider = provider;

    V->params.setGetRights.read = Read ? 1 : 0;
    V->params.setGetRights.write = Write ? 1 : 0;
    V->params.setGetRights.delete = Delete ? 1 : 0;
    V->params.setGetRights.rename = Rename ? 1 : 0;

    rc = ExecuteStatement(V->stmts.setRights);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        return(FALSE);
    }

    /* Get the children */
    PrepareStatement(getChildrenIDs, SQL_GET_CHILDREN_IDS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(getChildrenIDs, 1, &trustee);
        BindLongResult(getChildrenIDs, 1, &child);
    }

    if (getChildrenIDs == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    rc = ExecuteStatement(getChildrenIDs);
    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(getChildrenIDs);
    }

    while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        if (!MDBodbcSetObjectRightsByID(child, object, TRUE, provider, Read, Write, Delete, Rename, V)) {
            ReleaseStatement(V, getChildrenIDs);
            return(FALSE);
        }

        rc = SQLFetch(getChildrenIDs);
    }

    ReleaseStatement(V, getChildrenIDs);
    if (rc != SQL_NO_DATA) {
        return(FALSE);
    }

    return(TRUE);
}

static __inline BOOL
MDBodbcRemoveObjectRightsByID(SQLINTEGER trustee, SQLINTEGER object, MDBValueStruct *V)
{
    SQLHSTMT getChildrenIDs = SQL_NULL_HSTMT;
    SQLINTEGER child;
    SQLINTEGER rc;
    BOOL requireBindings;

    PrepareStatement(V->stmts.delRightsByObject, SQL_DEL_RIGHTS_BY_OBJECT, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.delRightsByObject, 1, &V->params.delRightsByObject.object);
        BindLongInput(V->stmts.delRightsByObject, 2, &V->params.delRightsByObject.trustee);
    }

    V->params.delRightsByObject.object = object;
    V->params.delRightsByObject.trustee = trustee;

    rc = ExecuteStatement(V->stmts.delRightsByObject);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
        return(FALSE);
    }

    /* Get the children */
    PrepareStatement(getChildrenIDs, SQL_GET_CHILDREN_IDS, requireBindings, V);
    if (requireBindings) {
        BindLongInput(getChildrenIDs, 1, &trustee);
        BindLongResult(getChildrenIDs, 1, &child);
    }

    if (getChildrenIDs == SQL_NULL_HSTMT) {
        return(FALSE);
    }

    rc = ExecuteStatement(getChildrenIDs);
    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(getChildrenIDs);
    }

    while (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        if (!MDBodbcRemoveObjectRightsByID(child, object, V)) {
            ReleaseStatement(V, getChildrenIDs);
            return(FALSE);
        }

        rc = SQLFetch(getChildrenIDs);
    }

    ReleaseStatement(V, getChildrenIDs);
    if (rc != SQL_NO_DATA) {
        return(FALSE);
    }

    return(TRUE);
}

BOOL 
MDBodbcGrantObjectRights(const unsigned char *Object, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Delete, BOOL Rename, BOOL Admin, MDBValueStruct *V)
{
    BOOL requireBindings;
    SQLINTEGER rc;
    SQLINTEGER object;
    SQLINTEGER trustee;
    BOOL r;
    BOOL w;
    BOOL d;
    BOOL n;
    BOOL allowed;
    unsigned long used;

    /*
        Ensure that both objects exist, and that we are allowed to write to the trustee
        object.  We will also get the rights that the current user has to the object, to
        ensure that no rights are given that the current user does not have.
    */
    if (!Object || !TrusteeDN || !V ||
        !MDBodbcGetObjectID(Object, &object, NULL, &r, &w, &d, &n, V) ||
        !MDBodbcGetObjectID(TrusteeDN, &trustee, NULL, NULL, &allowed, NULL, NULL, V) ||
        !allowed)
    {
        return(FALSE);
    }

    used = V->Used;

    if ((Read && !r) || (Write && !w) || (Delete && !d) || (Rename && !n)) {
        /* An attempt was made to give a right that the current user doesn't have */
        return(FALSE);
    }

    /*
        Ok, we now have the IDs of both objects, and we know that we are allowed to perform
        this operation.  First remove any right assignment that may already be in place
        between this trustee and this object.  By removing all rights to this object, where
        the provider matches the trustee we can be sure this will work for all cases (child
        objects, groups, security equals, etc)
    */
    PrepareStatement(V->stmts.delRightsByProviderAndObject, SQL_DEL_RIGHTS_BY_PROVIDER_AND_OBJ, requireBindings, V);
    if (requireBindings) {
        BindLongInput(V->stmts.delRightsByProviderAndObject, 1, &V->params.delRightsByProviderAndObject.provider);
        BindLongInput(V->stmts.delRightsByProviderAndObject, 2, &V->params.delRightsByProviderAndObject.object);
    }

    if (V->stmts.delRightsByProviderAndObject != SQL_NULL_HSTMT) {
        rc = ExecuteStatement(V->stmts.delRightsByProviderAndObject);

        if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO && rc != SQL_NO_DATA) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
            return(FALSE);
        }
    }

    if (!MDBodbcRemoveObjectRightsByID(trustee, object, V)) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }

    /* Update the rights on group members */
    MDBodbcRead(Object, "Member", V);

    while (V->Used > used) {
        SQLINTEGER id;

        if (MDBodbcGetObjectID(V->Value[used], &id, NULL, NULL, NULL, NULL, NULL, V) &&
            !MDBodbcSetObjectRightsByID(id, object, FALSE, trustee, Read, Write, Delete, Rename, V))
        {
            while (V->Used > used) {
                MDBodbcFreeValue(used, V);
            }
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
            return(FALSE);
        }

        MDBodbcFreeValue(used, V);
    }

    /* Alright, now its time to do the same thing, except to assign rights this time.  */
    if (MDBodbcSetObjectRightsByID(trustee, object, FALSE, trustee, Read, Write, Delete, Rename, V)) {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
        return(TRUE);
    } else {
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        return(FALSE);
    }
}

/* MDB-ODBC does not support MDBGrantAttributeRights.  As far as I can tell nothing uses it anyway. */
BOOL 
MDBodbcGrantAttributeRights(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Admin, MDBValueStruct *V)
{
    return(TRUE);
}

static BOOL 
MDBodbcParseArguments(const unsigned char *Arguments)
{
    unsigned char *toFree;
    unsigned char *ptr;
    unsigned char *end;
    BOOL result = TRUE;

    toFree = strdup(Arguments);
    ptr = toFree;

    while (ptr) {
        end = ptr + 1;
        do {
            end = strchr(end + 1, ',');
        } while (end && end[-1] == '\\');

        if (end) {
            *end = '\0';
        }

        if (XplStrNCaseCmp(ptr, "DSN=", 4) == 0) {
            strncpy(MDBodbc.config.dsn, ptr + 4, sizeof(MDBodbc.config.dsn));
        } else if (XplStrNCaseCmp(ptr, "user=", 5) == 0) {
            strncpy(MDBodbc.config.user, ptr + 5, sizeof(MDBodbc.config.user));
        } else if (XplStrNCaseCmp(ptr, "pass=", 5) == 0) {
            strncpy(MDBodbc.config.pass, ptr + 5, sizeof(MDBodbc.config.pass));
        } else if (XplStrNCaseCmp(ptr, "servername=", 11) == 0) {
            strncpy(MDBodbc.config.servername, ptr + 11, sizeof(MDBodbc.config.servername));
        } else if (XplStrNCaseCmp(ptr, "treename=", 9) == 0) {
            strncpy(MDBodbc.config.treename, ptr + 9, sizeof(MDBodbc.config.treename));
        }

        ptr = end;
        if (ptr) {
            ptr++;
        }
    }

    if (toFree) {
        free(toFree);
    }

    return(result);
}

/*
    Connect to the DB and ensure that all tables have been created, and that an
    object exists for this server.
*/
static BOOL 
MDBodbcSetup()
{
    MDBHandle handle = MDBodbcAuthenticate(NULL, NULL, NULL);
    MDBValueStruct *V = (handle) ? MDBodbcCreateValueStruct(handle, NULL) : NULL;
    SQLHSTMT stmt = SQL_NULL_HSTMT;
    SQLINTEGER rc;
    BOOL requireBindings;
    BOOL result = TRUE;
    unsigned long i = 0;

    if (!V) {
        if (handle) {
            MDBodbcRelease(handle);
        }

        XplConsolePrintf("MDBODBC: Failed to connect to database\n");

        return(FALSE);
    }

    /* Assume that the table does not exist */
    while (result && MDBodbcSQLPrepare[i]) {
        PrepareStatement(stmt, MDBodbcSQLPrepare[i], requireBindings, V);

        rc = ExecuteStatement(stmt);
        ReleaseStatement(V, stmt);
        stmt = SQL_NULL_HSTMT;

        /*
            Only return FALSE if it is the first line that fails.  There are some other
            lines which are expected to fail.  A few of the table definitions work differently
            for one database than they do for others, so both versions are included, and one
            will fail.
        */
        if (i == 0 && rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);

            result = FALSE;
        }

        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
        i++;
    }

    /* Commit the new tables */
    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);

    if (result) {
        /* Prepare rights for setup.  These rights will allow full access, until they are removed later */
        i = 0;

        while (result && MDBodbcSQLSetPrepareRights[i]) {
            SQLSMALLINT count;

            PrepareStatement(stmt, MDBodbcSQLSetPrepareRights[i], requireBindings, V);
            rc = SQLNumParams(stmt, &count);

            if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                if (count == 1) {
                    /* We know that the only param required is the treename */
                    MDBodbc.config.treenameLength = strlen(MDBodbc.config.treename);
                    BindCharInput(stmt, 1, MDBodbc.config.treename, &MDBodbc.config.treenameLength, MDB_ODBC_NAME_SIZE);
                }

                rc = ExecuteStatement(stmt);
            }

            ReleaseStatement(V, stmt);
            stmt = SQL_NULL_HSTMT;

            if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);

                result = FALSE;
            }

            i++;
        }
        SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);

        /* Define the default attributes */
        if (result) {
            MDBodbcBaseSchemaAttributes *attribute;

            /* End the transaction */
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);

            /* Define the default attributes */
            attribute = &MDBodbcBaseSchemaAttributesList[0];
            while (result && (attribute->name != NULL)) {
                result = MDBodbcDefineAttribute(attribute->name, NULL, attribute->type,
                    attribute->flags & 0x0001, attribute->flags & 0x0040, attribute->flags & 0x0080, V);

                attribute++;
            }
        }

        /* Define the default classes */
        if (result) {
            MDBValueStruct *superclass = MDBodbcShareContext(V);
            MDBValueStruct *containedby = MDBodbcShareContext(V);
            MDBValueStruct *naming = MDBodbcShareContext(V);
            MDBValueStruct *mandatory = MDBodbcShareContext(V);
            MDBValueStruct *optional = MDBodbcShareContext(V);
            MDBodbcBaseSchemaClasses *c;
            MDBodbcBaseSchemaClasses *cSource;
            unsigned char *ptr;
            unsigned char *cur;

            c = (MDBodbcBaseSchemaClasses *)malloc(sizeof(MDBodbcBaseSchemaClasses));
            if (superclass && containedby && naming && mandatory && optional && c) {
                cSource = &MDBodbcBaseSchemaClassesList[0];
                while (result && (cSource->name != NULL)) {
                    c->flags = cSource->flags;

                    c->name = strdup(cSource->name);

                    if (cSource->superClass) {
                        c->superClass = strdup(cSource->superClass);
                    } else {
                        c->superClass = NULL;
                    }

                    if (cSource->containedBy) {
                        c->containedBy = strdup(cSource->containedBy);
                    } else {
                        c->containedBy = NULL;
                    }

                    if (cSource->namedBy) {
                        c->namedBy = strdup(cSource->namedBy);
                    } else {
                        c->namedBy = NULL;
                    }

                    if (cSource->mandatory) {
                        c->mandatory = strdup(cSource->mandatory);
                    } else {
                        c->mandatory = NULL;
                    }

                    if (cSource->optional) {
                        c->optional = strdup(cSource->optional);
                    } else {
                        c->optional = NULL;
                    }

                    if (c->superClass) {
                        MDBodbcAddValue(c->superClass, superclass);
                    }

                    if (c->containedBy) {
                        ptr = c->containedBy;

                        do {
                            cur = ptr;
                            ptr = strchr(cur, ',');
                            if (ptr) {
                                *ptr = '\0';
                            }

                            MDBodbcAddValue(cur, containedby);

                            if (ptr) {
                                *ptr = ',';

                                ptr++;
                            }
                        } while (ptr);
                    }

                    if (c->namedBy) {
                        ptr = c->namedBy;

                        do {
                            cur = ptr;
                            ptr = strchr(cur, ',');
                            if (ptr) {
                                *ptr = '\0';
                            }

                            MDBodbcAddValue(cur, naming);

                            if (ptr) {
                                *ptr = ',';

                                ptr++;
                            }
                        } while (ptr);
                    }

                    if (c->mandatory) {
                        ptr = c->mandatory;

                        do {
                            cur = ptr;
                            ptr = strchr(cur, ',');
                            if (ptr) {
                                *ptr = '\0';
                            }

                            MDBodbcAddValue(cur, mandatory);

                            if (ptr) {
                                *ptr = ',';

                                ptr++;
                            }
                        } while (ptr);
                    }

                    if (c->optional) {
                        ptr = c->optional;

                        do {
                            cur = ptr;
                            ptr = strchr(cur, ',');
                            if (ptr) {
                                *ptr = '\0';
                            }

                            MDBodbcAddValue(cur, optional);

                            if (ptr) {
                                *ptr = ',';

                                ptr++;
                            }
                        } while (ptr);
                    }

                    result = MDBodbcDefineClass(c->name, NULL, c->flags & 0x01, superclass, containedby, naming, mandatory, optional, V);

                    free(c->name);
                    c->name = NULL;

                    if (c->superClass) {
                        free(c->superClass);
                        c->superClass = NULL;
                    }

                    if (c->containedBy) {
                        free(c->containedBy);
                        c->containedBy = NULL;
                    }

                    if (c->namedBy) {
                        free(c->namedBy);
                        c->namedBy = NULL;
                    }

                    if (c->mandatory) {
                        free(c->mandatory);
                        c->mandatory = NULL;
                    }

                    if (c->optional) {
                        free(c->optional);
                        c->optional = NULL;
                    }

                    MDBodbcFreeValues(optional);
                    MDBodbcFreeValues(mandatory);
                    MDBodbcFreeValues(naming);
                    MDBodbcFreeValues(containedby);
                    MDBodbcFreeValues(superclass);

                    cSource++;
                }
            }

            if (optional) {
                MDBodbcDestroyValueStruct(optional);
            }

            if (mandatory) {
                MDBodbcDestroyValueStruct(mandatory);
            }

            if (naming) {
                MDBodbcDestroyValueStruct(naming);
            }

            if (containedby) {
                MDBodbcDestroyValueStruct(containedby);
            }

            if (superclass) {
                MDBodbcDestroyValueStruct(superclass);
            }

            if (c) {
                free(c);
            }
        }

        /*
            Create the root of the tree.  This must be done after creating the default
            class list, but before creating any other objects.  This object has an id
            of -1.  It is the only object with a fixed id, so that we always know where
            the root of the tree is.  It is also listed as its own parent.  This way if
            a DN is given with a format of \Tree\Blah\Foo or \Blah\Foo they will both
            work correctly.
        */
        if (result) {
            i = 0;

            while (result && MDBodbcSQLCreateRoot[i]) {
                SQLSMALLINT count;

                PrepareStatement(stmt, MDBodbcSQLCreateRoot[i], requireBindings, V);
                rc = SQLNumParams(stmt, &count);

                if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
                    if (count == 1) {
                        /* We know that the only param required is the treename */
                        MDBodbc.config.treenameLength = strlen(MDBodbc.config.treename);
                        BindCharInput(stmt, 1, MDBodbc.config.treename, &MDBodbc.config.treenameLength, MDB_ODBC_NAME_SIZE);
                    }

                    rc = ExecuteStatement(stmt);
                }

                ReleaseStatement(V, stmt);
                stmt = SQL_NULL_HSTMT;

                if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);

                    result = FALSE;
                }

                i++;
            }

            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_COMMIT);
        }

        /*
            Now that we have created the schema, we need to destroy the value struct we have been using
            and create a new one.  The cached data in that value struct will now be incorrect because there
            was no schema when it was created.
        */
        MDBodbcDestroyValueStruct(V);
        V = MDBodbcCreateValueStruct(handle, NULL);

        /* Create the base objects */
        if (result) {
            if (!MDBodbcCreateObject("\\System",                    "Organization",  NULL, NULL, V) ||
                !MDBodbcCreateObject("\\System\\admin",             "User",          NULL, NULL, V) ||
                !MDBodbcCreateObject("\\System\\administrators",    "Group",         NULL, NULL, V))
            {
                XplConsolePrintf("Failed to create default objects\n");

                result = FALSE;
            }

            if (result) {
                result = MDBodbcChangePasswordEx("\\System\\admin", NULL, MDBodbc.config.pass, V);
            }

            if (result) {
                MDBodbcSetValueStructContext("\\System", V);
                result = MDBodbcCreateObject(MDBodbc.config.servername, "NCP Server",    NULL, NULL, V);
            }
        }

        if (result) {
            /* Create the tree info entry */
            MDBodbcGetObjectID("\\System", &MDBodbc.config.defaultContext, NULL, NULL, NULL, NULL, NULL, V);

            /* Create the tree info entry */
            PrepareStatement(V->stmts.setTreeInfo, SQL_SET_TREE_INFO, requireBindings, V);
            if (requireBindings) {
                MDBodbc.config.treenameLength = strlen(MDBodbc.config.treename);
                BindCharInput(V->stmts.setTreeInfo, 1,
                    &MDBodbc.config.treename, &MDBodbc.config.treenameLength, sizeof(MDBodbc.config.treename));
                BindLongInput(V->stmts.setTreeInfo, 2, &MDBodbc.config.defaultContext);
            }

            rc = ExecuteStatement(V->stmts.setTreeInfo);
            result = (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO);
            ReleaseStatement(V, stmt);
        }

        if (result) {
            i = 0;

            while (result && MDBodbcSQLSetInitialRights[i]) {
                PrepareStatement(stmt, MDBodbcSQLSetInitialRights[i], requireBindings, V);
                rc = ExecuteStatement(stmt);

                ReleaseStatement(V, stmt);
                stmt = SQL_NULL_HSTMT;

                if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
                    SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);

                    result = FALSE;
                }

                i++;
            }
        }

        if (!result) {
            SQLEndTran(SQL_HANDLE_DBC, V->handle->connection, SQL_ROLLBACK);
        }

        MDBodbcDestroyValueStruct(V);
        MDBodbcRelease(handle);
        ReleaseStatement(V, stmt);

        return(result);
    } else {
        /* The tree is already setup, just read the tree info */
        PrepareStatement(V->stmts.getTreeInfo, SQL_GET_TREE_INFO, requireBindings, V);
        if (requireBindings) {
            BindCharResult(V->stmts.getTreeInfo, 1, MDBodbc.config.treename, sizeof(MDBodbc.config.treename));
            BindLongResult(V->stmts.getTreeInfo, 2, &MDBodbc.config.defaultContext);
        }

        rc = ExecuteStatement(V->stmts.getTreeInfo);
        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            rc = SQLFetch(V->stmts.getTreeInfo);
        }

        if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
            SQLCloseCursor(V->stmts.getTreeInfo);
     
        }
    }

    /*
        Get the ID of the 'Member' attribute.  It is a special attribute in that we
        need to update rights whenever it is written to (or cleared).
    */
    PrepareStatement(V->stmts.getAttrByName, SQL_GET_ATTRIBUTE_BY_NAME, requireBindings, V);
    if (requireBindings) {
        BindCharInput(V->stmts.getAttrByName, 1,
            V->params.getAttrByName.attribute, &V->params.getAttrByName.attributeLength, MDB_ODBC_NAME_SIZE);
        BindLongResult(V->stmts.getAttrByName, 1, &V->params.getAttrByName.id);
    }

    StoreCharInput("Member", V->params.getAttrByName.attribute, V->params.getAttrByName.attributeLength, MDB_ODBC_NAME_SIZE);

    /* Ok, we're all setup - Do the query */
    rc = ExecuteStatement(V->stmts.getAttrByName);

    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        rc = SQLFetch(V->stmts.getAttrByName);
        SQLCloseCursor(V->stmts.getAttrByName);
    }

    /* Cleanup */
    MDBodbcDestroyValueStruct(V);
    MDBodbcRelease(handle);

    if (rc == SQL_SUCCESS || rc == SQL_SUCCESS_WITH_INFO) {
        MDBodbc.config.special.member = V->params.getAttrByName.id;
        return(TRUE);
    } else {
        return(FALSE);
    }
}

EXPORT BOOL 
MDBODBCInit(MDBDriverInitStruct *Init)
{
    MDBodbc.unload = FALSE;
    SQLINTEGER rc;

    /* Allocate the ODBC environment */
    MDBodbc.env = SQL_NULL_HENV;
    rc = SQLAllocHandle(SQL_HANDLE_ENV, SQL_NULL_HANDLE, &MDBodbc.env);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
        return(FALSE);
    }

    rc = SQLSetEnvAttr(MDBodbc.env, SQL_ATTR_ODBC_VERSION, (SQLPOINTER)SQL_OV_ODBC3, SQL_IS_INTEGER);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) {
        SQLFreeHandle(SQL_HANDLE_ENV, MDBodbc.env);
        return(FALSE);
    }

    /*
        Setup some defaults

        If a username or password isn't provided in Init->Arguments we'll have to assume that the
        DSN is already configured with a proper username and password.
    */
    memset(&MDBodbc.config, 0, sizeof(MDBodbc.config));
    strcpy(MDBodbc.config.dsn, "MDB");
    strcpy(MDBodbc.config.treename, "Tree");

    gethostname(MDBodbc.config.servername, sizeof(MDBodbc.config.servername));

    if (Init->Arguments) {
        MDBodbcParseArguments(Init->Arguments);
    }

    Init->Interface.MDBAdd                      = MDBodbcAdd;
    Init->Interface.MDBAddAttribute             = MDBodbcAddAttribute;
    Init->Interface.MDBAddDN                    = MDBodbcAddDN;
    Init->Interface.MDBAddValue                 = MDBodbcAddValue;
    Init->Interface.MDBAuthenticate             = MDBodbcAuthenticate;
    Init->Interface.MDBChangePassword           = MDBodbcChangePassword;
    Init->Interface.MDBChangePasswordEx         = MDBodbcChangePasswordEx;
    Init->Interface.MDBClear                    = MDBodbcClear;
    Init->Interface.MDBCreateAlias              = MDBodbcCreateAlias;
    Init->Interface.MDBCreateEnumStruct         = MDBodbcCreateEnumStruct;
    Init->Interface.MDBCreateObject             = MDBodbcCreateObject;
    Init->Interface.MDBCreateValueStruct        = MDBodbcCreateValueStruct;
    Init->Interface.MDBDefineAttribute          = MDBodbcDefineAttribute;
    Init->Interface.MDBDefineClass              = MDBodbcDefineClass;
    Init->Interface.MDBDeleteObject             = MDBodbcDeleteObject;
    Init->Interface.MDBDestroyEnumStruct        = MDBodbcDestroyEnumStruct;
    Init->Interface.MDBDestroyValueStruct       = MDBodbcDestroyValueStruct;
    Init->Interface.MDBEnumerateAttributesEx    = MDBodbcEnumerateAttributesEx;
    Init->Interface.MDBEnumerateObjects         = MDBodbcEnumerateObjects;
    Init->Interface.MDBEnumerateObjectsEx       = MDBodbcEnumerateObjectsEx;
    Init->Interface.MDBFreeValue                = MDBodbcFreeValue;
    Init->Interface.MDBFreeValues               = MDBodbcFreeValues;
    Init->Interface.MDBGetObjectDetails         = MDBodbcGetObjectDetails;
    Init->Interface.MDBGetObjectDetailsEx       = MDBodbcGetObjectDetailsEx;
    Init->Interface.MDBGetServerInfo            = MDBodbcGetServerInfo;
    Init->Interface.MDBGetBaseDN                = MDBodbcGetBaseDN;
    Init->Interface.MDBGrantAttributeRights     = MDBodbcGrantAttributeRights;
    Init->Interface.MDBGrantObjectRights        = MDBodbcGrantObjectRights;
    Init->Interface.MDBIsObject                 = MDBodbcIsObject;
    Init->Interface.MDBListContainableClasses   = MDBodbcListContainableClasses;
    Init->Interface.MDBListContainableClassesEx = MDBodbcListContainableClassesEx;
    Init->Interface.MDBRead                     = MDBodbcRead;
    Init->Interface.MDBReadDN                   = MDBodbcRead;
    Init->Interface.MDBReadEx                   = MDBodbcReadEx;
    Init->Interface.MDBRelease                  = MDBodbcRelease;
    Init->Interface.MDBRemove                   = MDBodbcRemove;
    Init->Interface.MDBRemoveDN                 = MDBodbcRemoveDN;
    Init->Interface.MDBRenameObject             = MDBodbcRenameObject;
    Init->Interface.MDBSetValueStructContext    = MDBodbcSetValueStructContext;
    Init->Interface.MDBShareContext             = MDBodbcShareContext;
    Init->Interface.MDBUndefineAttribute        = MDBodbcUndefineAttribute;
    Init->Interface.MDBUndefineClass            = MDBodbcUndefineClass;
    Init->Interface.MDBVerifyPassword           = MDBodbcVerifyPassword;
    Init->Interface.MDBVerifyPasswordEx         = MDBodbcVerifyPassword;
    Init->Interface.MDBWrite                    = MDBodbcWrite;
    Init->Interface.MDBWriteDN                  = MDBodbcWriteDN;
    Init->Interface.MDBWriteTyped               = MDBodbcWriteTyped;

    if (!MDBodbcSetup()) {
        SQLFreeHandle(SQL_HANDLE_ENV, MDBodbc.env);
        return(FALSE);
    } else {
        return(TRUE);
    }
}

EXPORT void 
MDBODBCShutdown(void)
{
    SQLFreeHandle(SQL_HANDLE_ENV, MDBodbc.env);
    MDBodbc.unload = TRUE;
    return;
}

#if defined(NETWARE) || defined(LIBC)
int 
MDBodbcRequestUnload(void)
{
    if (!MDBodbc.unload) {
        int    OldTGid;

        OldTGid=XplSetThreadGroupID(MDBodbc.id.group);

        XplConsolePrintf("\rThis NLM will automatically be unloaded by the thread that loaded it.\n");
        XplConsolePrintf("\rIt does not allow manual unloading.\n");
        XplSetCurrentScreen(XplCreateScreen("System Console", 0));
        XplUngetCh('n');

        XplSetThreadGroupID(OldTGid);

        return(1);
    }

    return(0);
}

void 
MDBodbcSigHandler(int Signal)
{
    int    OldTGid;

    OldTGid=XplSetThreadGroupID(MDBodbc.id.group);

    XplSignalLocalSemaphore(MDBodbc.sem.shutdown);
    XplWaitOnLocalSemaphore(MDBodbc.sem.shutdown);

    /* Do any required cleanup */
    MDBodbcShutdown();

    XplCloseLocalSemaphore(MDBodbc.sem.shutdown);
    XplSetThreadGroupID(OldTGid);
}

int 
main(int argc, char *argv[])
{
    MDBodbc.id.main=XplGetThreadID();
    MDBodbc.id.group=XplGetThreadGroupID();

    signal(SIGTERM, MDBodbcSigHandler);

    /*
        This will "park" the module 'til we get unloaded; 
        it would not be neccessary to do this on NetWare, 
        but to prevent from automatically exiting on Unix
        we need to keep main around...
    */

    XplOpenLocalSemaphore(MDBodbc.sem.shutdown, 0);
    XplWaitOnLocalSemaphore(MDBodbc.sem.shutdown);
    XplSignalLocalSemaphore(MDBodbc.sem.shutdown);
    return(0);
}
#endif

#ifdef WIN32
BOOL WINAPI
DllMain(HINSTANCE hInst, DWORD Reason, LPVOID Reserved)
{
    if (Reason == DLL_PROCESS_ATTACH) {
        DisableThreadLibraryCalls(hInst);
    }

    return(TRUE);
}
#endif

/* StreamIO needs this */
BOOL 
MWHandleNamedTemplate(void *ignored1, unsigned char *ignored2, void *ignored3)
{
    return(FALSE);
}
