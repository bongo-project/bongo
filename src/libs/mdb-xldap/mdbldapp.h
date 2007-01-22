#define LDAP_DEPRECATED 1
#include <ldap.h>
#include <config.h>
#include <xpl.h>

#define MDBLDAP_FLAGS_ALLOCATED_HANDLE (unsigned long)(1 << 0)
#define MDBLDAP_FLAGS_CONTEXT_VALID (unsigned long)(1 << 1)
#define MDBLDAP_FLAGS_CONTEXT_DUPLICATE (unsigned long)(1 << 2)
#define MDBLDAP_FLAGS_BASEDB_CHANGED (unsigned long)(1 << 3)

#define MDBLDAP_MIN_CONNECTIONS 4
#define MDBLDAP_MAX_CONNECTIONS 8

#define MDBLDAP_DEFAULT_CACHE_SIZE 512

#define VALUE_ALLOC_SIZE 20

typedef struct {
    unsigned char Description[128];
    struct _MDBInterfaceStruct *Interface;
    char *binddn;
    char *passwd;

    struct {
        int next;
        int min;
        int max;
        XplMutex mutex;
        LDAP **list;
    } conn;
} MDBLDAPContextStruct;

typedef MDBLDAPContextStruct *MDBHandle;

typedef struct {
    unsigned char **Value;
    unsigned long Used;
    unsigned long ErrNo;
    struct _MDBInterfaceStruct *Interface;

    MDBLDAPContextStruct *context;
    LDAP *ldap;
    unsigned char *base;
    unsigned char *lastDn;
    unsigned long allocated;
    BOOL duplicate;
} MDBValueStruct;

typedef struct {
    char **vals;
    int next;
    int count;
} MDBEnumStruct;

#ifndef MDB_INTERNAL
#define MDB_INTERNAL
#endif

#include <mdb.h>

/* schema.c */

typedef struct {
    char *val;
    char *oid;
    char **names;
    int namecnt;
    char *desc;
    char *equality;
    char *syntax;
    char *sup;
    BOOL singleval;
    int refcnt;
    BOOL visited;
} MDBLDAPSchemaAttr;

typedef enum {
    TYPE_ABSTRACT,
    TYPE_AUXILIARY,
    TYPE_STRUCTURAL
} ClassTypeEnum;

typedef struct {
    char *val;
    char *oid;
    char **names;
    char *desc;
    char **sup;
    char **must;
    char **may;
    char *naming;
    ClassTypeEnum type;
    int refcnt;
    BOOL visited;
} MDBLDAPSchemaClass;

#define MDBLDAP_SYNTAX_DN(syntax) \
    !strncmp((syntax), "1.3.6.1.4.1.1466.115.121.1.12", 29)

#define MDBLDAP_SYNTAX_STRING(syntax) \
    !strncmp((syntax), "1.3.6.1.4.1.1466.115.121.1.15", 29)

BOOL ParseAttributeTypeDescription(char *ptr, MDBLDAPSchemaAttr *attr);
BOOL ParseObjectClassDescription(char *ptr, MDBLDAPSchemaClass *class);

/* valarray.c */
typedef char * (* TranslateFunction) (const char *val, const char *opt);
inline char **NewValArray(int size);
inline void FreeValArray(char **vals);
inline int CountValArray(char **vals);
inline char **CopyValArray(char **vals);
inline BOOL ExpandValArray(char ***vals, int size);
inline unsigned int ValueStructToValArray(MDBValueStruct *v, char **vals);
inline int ValArrayToValueStruct(char **vals, MDBValueStruct *v);
inline char **TranslateValArray(char **in, TranslateFunction transfunc, char *opt);
inline unsigned int TranslateValueStructToValArray(MDBValueStruct *v, char **vals, TranslateFunction transfunc, char *opt);
inline int TranslateValArrayToValueStruct(char **vals, MDBValueStruct *v, TranslateFunction transfunc, char *opt);

BOOL MDBLDAPAddValue(const unsigned char *Value, MDBValueStruct *V);
BOOL MDBLDAPFreeValues(MDBValueStruct * V);

EXPORT BOOL MDBLDAPInit(MDBDriverInitStruct *Init);
EXPORT void MDBLDAPShutdown(void);
