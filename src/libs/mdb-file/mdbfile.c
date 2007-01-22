/*
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
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
 */

/* Product defines */
#define PRODUCT_NAME        "MDB Filesystem Driver"
#define PRODUCT_VERSION     "$Revision:   1.1  $"

#include <config.h>
#include <xpl.h>
#include <bongoutil.h>

#include <errno.h>
#include <ctype.h>

#include "mdbfilep.h"
#include <mdb.h>

#define    ChopNL(String)   { register unsigned char *p = strchr((String), 0x0a);    if (p) {*p = '\0'; if (*(p - 1) == 0x0d) {*(p - 1)='\0'; }}}

#if !defined(DEBUG)
#define    LogMessage       if (MDBFile.log.enabled) MDBFILELogMessage
#else
#define    LogMessage       MDBFILELogMessage
#endif

typedef struct _MDBFILESchemaAttribute {
    unsigned long flags;
    unsigned long syntax;

    unsigned char asn1[36];
    unsigned char name[MDB_MAX_ATTRIBUTE_CHARS];
    unsigned char filename[(XPL_MAX_PATH * 3) + 1];
    unsigned char buffer[VALUE_BUFFER_SIZE];
} MDBFILESchemaAttribute;

typedef struct _MDBFILESchemaClass {
    unsigned long flags;

    unsigned char asn1[256];
    unsigned char name[MDB_MAX_OBJECT_CHARS];
    unsigned char filename[(XPL_MAX_PATH * 3) + 1];
    unsigned char buffer[VALUE_BUFFER_SIZE];

    MDBValueStruct *defaultACL;
    MDBValueStruct *superClass;
    MDBValueStruct *contains;
    MDBValueStruct *containedBy;
    MDBValueStruct *naming;
    MDBValueStruct *mandatory;
    MDBValueStruct *optional;
} MDBFILESchemaClass;

struct  {
    BOOL unload;

    struct {
        XplSemaphore shutdown;
    } sem;

    struct {
        XplThreadID main;
        XplThreadID group;
    } id;

    unsigned char localTree[MDB_MAX_TREE_CHARS + 1];
    unsigned char replicaDN[MDB_MAX_OBJECT_CHARS + 1];
    unsigned char root[XPL_MAX_PATH + 1];
    unsigned char schema[XPL_MAX_PATH + 1];
    unsigned char base64Chars[66];

    unsigned int rootLength;

    struct {
        BOOL enabled;
        BOOL console;

        FILE *fh;

        XplSemaphore sem;

        int bufferSize;

        unsigned char *buffer;
    } log;

    MDBHandle directoryHandle;

    struct {
        MDBFILEContextStruct **data;
        unsigned long used;
        unsigned long allocated;
        XplSemaphore sem;
    } handles;
} MDBFile;

/*
    Prototypes for registration functions
*/
BOOL MDBFILEGetServerInfo(unsigned char *ServerDN, unsigned char *ServerTree, MDBValueStruct *V);
char *MDBFILEGetBaseDN(MDBValueStruct *V);
MDBHandle MDBFILEAuthenticateFunction(const unsigned char *Object, const unsigned char *Password, const unsigned char *Arguments);
BOOL MDBFILERelease(MDBHandle Context);
MDBValueStruct *MDBFILECreateValueStruct(MDBHandle Handle, const unsigned char *Context);
BOOL MDBFILEFreeValues(MDBValueStruct *V);
BOOL MDBFILEDestroyValueStruct(MDBValueStruct *V);
MDBEnumStruct *MDBFILECreateEnumStruct(MDBValueStruct *V);
BOOL MDBFILEDestroyEnumStruct(MDBEnumStruct *ES, MDBValueStruct *V);
BOOL MDBFILESetValueStructContext(const unsigned char *Context, MDBValueStruct *V);
MDBValueStruct *MDBFILEShareContext(MDBValueStruct *V);
BOOL MDBFILEAddValue(const unsigned char *Value, MDBValueStruct *V);
BOOL MDBFILEFreeValue(unsigned long Index, MDBValueStruct *V);
BOOL MDBFILEDefineAttribute(const unsigned char *Attribute, const unsigned char *ASN1, unsigned long Type, BOOL SingleValue, BOOL ImmediateSync, BOOL Public, MDBValueStruct *V);
BOOL MDBFILEAddAttribute(const unsigned char *Attribute, const unsigned char *Class, MDBValueStruct *V);
BOOL MDBFILEUndefineAttribute(const unsigned char *Attribute, MDBValueStruct *V);
BOOL MDBFILEDefineClass(const unsigned char *Class, const unsigned char *ASN1, BOOL Container, MDBValueStruct *Superclass, MDBValueStruct *Containment, MDBValueStruct *Naming, MDBValueStruct *Mandatory, MDBValueStruct *Optional, MDBValueStruct *V);
BOOL MDBFILEUndefineClass(const unsigned char *Class, MDBValueStruct *V);
BOOL MDBFILEListContainableClasses(const unsigned char *Object, MDBValueStruct *V);
BOOL MDBFILEOpenStream(const unsigned char *Object, const unsigned char *AttributeIn, MDBValueStruct *V);
BOOL MDBFILEReadStream(MDBValueStruct *V);
BOOL MDBFILEWriteStream(MDBValueStruct *V);
BOOL MDBFILECloseStream(MDBValueStruct *V);
BOOL MDBFILEWriteTyped(const unsigned char *Object, const unsigned char *Attribute, const int AttrType, MDBValueStruct *V);
BOOL MDBFILEWrite(const unsigned char *Object, const unsigned char *AttributeIn, MDBValueStruct *V);
BOOL MDBFILEWriteDN(const unsigned char *Object, const unsigned char *AttributeIn, MDBValueStruct *V);
BOOL MDBFILEAdd(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
BOOL MDBFILEAddDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
BOOL MDBFILERemove(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
BOOL MDBFILERemoveDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
BOOL MDBFILEClear(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
BOOL MDBFILEIsObject(const unsigned char *Object, MDBValueStruct *V);
BOOL MDBFILEGetObjectDetails(const unsigned char *Object, unsigned char *Type, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V);
BOOL MDBFILEGetObjectDetailsEx(const unsigned char *Object, MDBValueStruct *Types, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V);
BOOL MDBFILEVerifyPassword(const unsigned char *Object, const unsigned char *Password, MDBValueStruct *V);
BOOL MDBFILEChangePassword(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V);
BOOL MDBFILEChangePasswordEx(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V);
BOOL MDBFILECreateAlias(const unsigned char *Alias, const unsigned char *AliasedObjectDn, MDBValueStruct *V);
BOOL MDBFILECreateObject(const unsigned char *Object, const unsigned char *Class, MDBValueStruct *Attribute, MDBValueStruct *Data, MDBValueStruct *V);
BOOL MDBFILEDeleteObject(const unsigned char *Object, BOOL Recursive, MDBValueStruct *V);
BOOL MDBFILERenameObject(const unsigned char *ObjectOld, const unsigned char *ObjectNew, MDBValueStruct *V);
BOOL MDBFILEGrantObjectRights(const unsigned char *Object, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Delete, BOOL Rename, BOOL Admin, MDBValueStruct *V);
BOOL MDBFILEGrantAttributeRights(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Admin, MDBValueStruct *V);
BOOL MDBFILEAddAddressRestriction(const unsigned char *Object, const unsigned char *Server, MDBValueStruct *V);

const unsigned char *MDBFILEListContainableClassesEx(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V);
const unsigned char *MDBFILEReadEx(const unsigned char *Object, const unsigned char *Attribute, MDBEnumStruct *E, MDBValueStruct *V);
const unsigned char *MDBFILEEnumerateAttributesEx(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V);
const unsigned char *MDBFILEEnumerateObjectsEx(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, unsigned long Flags, MDBEnumStruct *E, MDBValueStruct *V);

long MDBFILERead(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
long MDBFILEReadDN(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
long MDBFILEEnumerateObjects(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, MDBValueStruct *V);

EXPORT BOOL MDBFILEInit(MDBDriverInitStruct *Init);
EXPORT void MDBFILEShutdown(void);

static void 
MDBFILELogMessage(const unsigned char *Format, ...)
{
    if (MDBFile.log.enabled) {
        int length;
        va_list arguments;

        XplWaitOnLocalSemaphore(MDBFile.log.sem);

        va_start(arguments, Format);
        vsprintf(MDBFile.log.buffer, Format, arguments);
        va_end(arguments);

        length = strlen(MDBFile.log.buffer);

        fwrite(MDBFile.log.buffer, sizeof(unsigned char), length, MDBFile.log.fh);

        if (MDBFile.log.console) {
            fwrite(MDBFile.log.buffer, sizeof(unsigned char), length, stdout);
        }

        XplSignalLocalSemaphore(MDBFile.log.sem);
    }

    return;
}

static int 
MDBFILEEncodePathname(const unsigned char *Source, unsigned char *Destination, unsigned char **EndNode)
{
    register const unsigned char *src = Source;
    register unsigned char *dest = Destination;

    do {
        if (islower(*src)) {
            *dest++ = *src++;

            continue;
        }

        if (isupper(*src)) {
            *dest++ = tolower(*src++);

            continue;
        }

        if ((*src == '/') || (*src == '\\')) {
            if (EndNode) {
                *EndNode = dest;
            }

            *dest++ = '/';
            src++;

            continue;
        }

        dest += sprintf(dest, "%%%03d", *src);
        src++;
    } while (*src);

    *dest = '\0';

    return(dest - Destination);
}

static int 
MDBFILEClassToFilename(const unsigned char *Class, unsigned char *Filename)
{
    register const unsigned char *src = Class;
    register unsigned char *dest = Filename;

    dest += sprintf(dest, "%s/classes/", MDBFile.schema);

    dest += MDBFILEEncodePathname(src, dest, NULL);

    return(dest - Filename);
}

static int 
MDBFILEAttributeToFilename(const unsigned char *Attribute, unsigned char *Filename)
{
    register const unsigned char *src = Attribute;
    register unsigned char *dest = Filename;

    dest += sprintf(dest, "%s/attributes/", MDBFile.schema);

    dest += MDBFILEEncodePathname(src, dest, NULL);

    return(dest - Filename);
}

static int 
MDBFILEObjectToFilename(const unsigned char *Object, const unsigned char *Attribute, unsigned char *Filename, MDBValueStruct *V, unsigned char **EndNode)
{
    unsigned char path[XPL_MAX_PATH + 1];
    register unsigned char *src = path;
    register unsigned char *dest = Filename;

    if (Object) {
        if (Object[0] != '\\') {
            if (V) {
                src += sprintf(src, "%s\\%s", V->BaseDN, Object);
            } else {
                src += sprintf(src, "%s\\%s", MDBFile.localTree, Object);
            }
        } else {
            src += sprintf(src, "%s", Object);
        }

        memcpy(dest, MDBFile.root, MDBFile.rootLength);
        dest += MDBFile.rootLength;
    }

    if (Attribute) {
        src += sprintf(src, "\\%s", Attribute);
    }

    src = path;

    dest += MDBFILEEncodePathname(src, dest, EndNode);

    return(dest - Filename);
}

static BOOL 
MDBFILEFilenameToObjectDN(unsigned char *Filename, unsigned char *DN, unsigned char **RDN)
{
    unsigned char *node = Filename;
    unsigned char *dn = DN;
    unsigned char *delim;
    unsigned char *oDelim;
    unsigned char *sDelim;
    unsigned char buffer[MDB_MAX_OBJECT_CHARS];
    unsigned char oPath[(XPL_MAX_PATH * 4) + 1];
    unsigned char sPath[(XPL_MAX_PATH * 4) + 1];
    FILE *oHandle;
    FILE *sHandle;
    BOOL result = TRUE;

    if (RDN) {
        *RDN = NULL;
    }

    oDelim = oPath + sprintf(oPath, "%s", MDBFile.root);
    sDelim = sPath + sprintf(sPath, "%s/classes/", MDBFile.schema);

    node = Filename + MDBFile.rootLength;
    while (result && node) {
        *dn++ = '\\';

        if (RDN) {
            *RDN = dn;
        }

        delim = strchr(++node, '/');
        if (delim) {
            *delim = '\0';
        }

        *oDelim++ = '/';
        strcpy(oDelim, node);
        oDelim += strlen(node);
        *oDelim++ = '/';

        result = FALSE;

        strcpy(oDelim, "object%032class");
        oHandle = fopen(oPath, "rb");
        if (oHandle) {
            if (fgets(buffer, sizeof(buffer), oHandle)) {
                ChopNL(buffer);

                MDBFILEEncodePathname(buffer, sDelim, NULL);

                result = TRUE;
            }

            fclose(oHandle);
        }

        if (result) {
            result = FALSE;

            strcat(sDelim, "/naming");
            sHandle = fopen(sPath, "rb");
            if (sHandle) {
                if (fgets(buffer, sizeof(buffer), sHandle)) {
                    ChopNL(buffer);

                    MDBFILEEncodePathname(buffer, oDelim, NULL);

                    result = TRUE;
                }

                fclose(sHandle);
            }
        }

        if (result) {
            result = FALSE;

            oHandle = fopen(oPath, "rb");
            if (oHandle) {
                if (fgets(dn, MDB_MAX_OBJECT_CHARS, oHandle)) {
                    ChopNL(dn);

                    dn += strlen(dn);

                    result = TRUE;
                }

                fclose(oHandle);
            }
        }

        if (delim) {
            *delim = '/';
        }

        node = delim;
    }

    return(result);
}

// Converts a filename based attribute name to its real name
// e.g. 'object%032class' -> 'Object Class'
// input:  Filename - takes a filename syntax 'cn'
// returns: DN - corrected syntax attribute name

static BOOL
MDBFILEFilenameToAttributeName(unsigned char *Filename, unsigned char *attrname)
{
    unsigned char *node = Filename;
    unsigned char *attr = attrname;
    unsigned char *delim;
    unsigned char *sDelim;
    unsigned char buffer[MDB_MAX_OBJECT_CHARS];
    unsigned char sPath[(XPL_MAX_PATH * 4) + 1];
    FILE *sHandle;
    BOOL result = TRUE;
    
    sDelim = sPath + sprintf(sPath, "%s/attributes/", MDBFile.schema);
                                                                                                                                          
    while (result && node) {
        
	delim = strchr(++node, '/');
        if (delim) {
            *delim = '\0';
        }
                                                                                                                                          	// get the /naming value from the scheme (e.g. cn, ou)
        if (result) {
	    result = FALSE;

	    strcpy(sDelim, Filename);
   	    strcat(sDelim, "/name");
	
            sHandle = fopen(sPath, "rb");
            if (sHandle) {
               if (fgets(buffer, sizeof(buffer), sHandle)) {
                    ChopNL(buffer);
	
		    strcpy(attr,buffer);
		    attr+= strlen(attr);

                    result = TRUE;
                }

                fclose(sHandle);
            }
        }

        if (delim) {
            *delim = '/';
        }

        node = delim;
    }

    return(result);
}


static __inline BOOL
MDBFileIsValidHandle(MDBHandle Handle)
{
    unsigned long i;

    for (i = 0; i < MDBFile.handles.used; i++) {
        if ((Handle) == (void *)MDBFile.handles.data[i]) {
            return(TRUE);
        }
    }

    return(FALSE);
}

static __inline BOOL
MDBFileIsReadWrite(MDBValueStruct *V)
{
    if (!V->Handle->ReadOnly) {
        return(TRUE);
    } else {
        unsigned char *e = strrchr(V->Filename, '/');

        /* Return TRUE for the object that created the handle. */
        return(e && (unsigned)(e - V->Filename) == (unsigned)strlen(V->Handle->Dirname) &&
            XplStrNCaseCmp(V->Handle->Dirname, V->Filename, e - V->Filename) == 0);
    }
}


BOOL 
MDBFILEGetServerInfo(unsigned char *ServerDN, unsigned char *ServerTree, MDBValueStruct *V)
{
    if (ServerDN) {
        strcpy(ServerDN, "Unknown");
    }

    if (ServerTree) {
        strcpy(ServerTree, MDBFile.localTree + 1);
    }

    return(TRUE);
}

char * 
MDBFILEGetBaseDN(MDBValueStruct *V)
{
    return(MDBFile.localTree);
}


static __inline MDBHandle
MDBFileCreateContext(unsigned char *Dirname, BOOL ReadOnly)
{
    MDBFILEContextStruct *handle;

    /* Create a new context */
    handle = malloc(sizeof(MDBFILEContextStruct));

    strcpy(handle->Dirname, Dirname);
    handle->ReadOnly = ReadOnly;

    XplWaitOnLocalSemaphore(MDBFile.handles.sem);

    /* Add the handle to the the list of handles, so that it can be verified later */
    if ((MDBFile.handles.used + 1) > MDBFile.handles.allocated) {
        void *ptr = realloc(MDBFile.handles.data, (MDBFile.handles.allocated + 100) * sizeof(MDBFILEContextStruct *));

        if (ptr) {
            MDBFile.handles.data = ptr;
            MDBFile.handles.allocated += 100;
            }
    }

    if ((MDBFile.handles.used + 1) <= MDBFile.handles.allocated) {
        MDBFile.handles.data[MDBFile.handles.used] = handle;
        MDBFile.handles.used++;

        XplSignalLocalSemaphore(MDBFile.handles.sem);

        return((void *)handle);
    }

    XplSignalLocalSemaphore(MDBFile.handles.sem);

    return(NULL);
}

BOOL MDBFILERelease(MDBHandle Context)
{
    unsigned long i;

    for (i = 0; i < MDBFile.handles.used; i++) {
        if (Context == (void *)MDBFile.handles.data[i]) {
            /* Free the context */
            XplWaitOnLocalSemaphore(MDBFile.handles.sem);

            free(MDBFile.handles.data[i]);

            if (i < (MDBFile.handles.used - 1)) {
                memmove(&MDBFile.handles.data[i], &MDBFile.handles.data[i + 1],
                    ((MDBFile.handles.used - 1) - i) * sizeof(MDBFILEContextStruct *));
            }

            MDBFile.handles.used--;

            XplSignalLocalSemaphore(MDBFile.handles.sem);

        return(TRUE);
    }
    }

    return(FALSE);
}


MDBValueStruct *
MDBFILECreateValueStruct(MDBHandle Handle, const unsigned char *Context)
{
    MDBValueStruct *v = NULL;

    if (MDBFileIsValidHandle(Handle)) {
        v = malloc(sizeof(MDBValueStruct));
        if (v) {
            memset(v, 0, sizeof(MDBValueStruct));

            v->Flags = MDB_FLAGS_CONTEXT_VALID;
            v->BaseDN = malloc(XPL_MAX_PATH + 1);
            v->Handle = Handle;

            if (!Context) {
                sprintf(v->BaseDN, "%s", MDBFile.localTree);
            } else {
                if (*Context != '\\') {
                    sprintf(v->BaseDN, "%s\\%s", MDBFile.localTree, Context);
                } else {
                    strcpy(v->BaseDN, Context);
                }

                v->Flags |= MDB_FLAGS_BASEDN_CHANGED;
            }
        }
    }

    return(v);
}

BOOL 
MDBFILEFreeValues(MDBValueStruct *V)
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
MDBFILEDestroyValueStruct(MDBValueStruct *V)
{
    if (V && MDB_CONTEXT_IS_VALID(V)) {
        if (!(V->Flags & MDB_FLAGS_CONTEXT_DUPLICATE)) {
            free(V->BaseDN);
        }

        if (V->Allocated) {
            MDBFILEFreeValues(V);

            if (V->Value) {
                free(V->Value);
            }
        }

        free(V);

        return(TRUE);
    }

    return(FALSE);
}

MDBEnumStruct *MDBFILECreateEnumStruct(MDBValueStruct *V)
{
    MDBEnumStruct *es = NULL;

    if (V && MDB_CONTEXT_IS_VALID(V)) {
        es = malloc(sizeof(MDBEnumStruct));
        if (es) {
            es->Initialized = FALSE;

            es->EntriesLeft = 0;

            es->File = NULL;
            es->V = NULL;
        }
    }

    return(es);
}

BOOL MDBFILEDestroyEnumStruct(MDBEnumStruct *ES, MDBValueStruct *V)
{
    if (ES) {
        if (ES->Initialized) {
            if (ES->File) {
                fclose(ES->File);
            }

            if (ES->V) {
                MDBFILEDestroyValueStruct(ES->V);
            }
        }

        free(ES);

        return(TRUE);    
    }

    return(FALSE);
}

BOOL MDBFILESetValueStructContext(const unsigned char *Context, MDBValueStruct *V)
{
    if (V && MDB_CONTEXT_IS_VALID(V)) {
        if (!Context || (*Context == '\0')) {
            sprintf(V->BaseDN, "%s", MDBFile.localTree);
        } else {
            if (*Context != '\\') {
                sprintf(V->BaseDN, "%s\\%s", MDBFile.localTree, Context);
            } else {
                strcpy(V->BaseDN, Context);
            }

            V->Flags |= MDB_FLAGS_BASEDN_CHANGED;
        }

        return(TRUE);
    }

    return(FALSE);
}

MDBValueStruct *MDBFILEShareContext(MDBValueStruct *V)
{
    MDBValueStruct    *n = NULL;

    if (V && MDB_CONTEXT_IS_VALID(V)) {
        n = malloc(sizeof(MDBValueStruct));
        if (n) {
            memset(n, 0, sizeof(MDBValueStruct));

            n->Flags = MDB_FLAGS_CONTEXT_DUPLICATE | V->Flags;
            n->BaseDN = V->BaseDN;
            n->Handle = V->Handle;
        }
    }

    return(n);
}

BOOL MDBFILEAddValue(const unsigned char *Value, MDBValueStruct *V)
{
    register unsigned char *ptr;

    if (Value && (*Value != '\0')) {
        if ((V->Used + 1) > V->Allocated) {
            ptr = (unsigned char    *)realloc(V->Value, (V->Allocated + VALUE_ALLOC_SIZE) * sizeof(unsigned char *));
            if (ptr) {
                V->Value = (unsigned char    **)ptr;
                V->Allocated += VALUE_ALLOC_SIZE;
            } else {
                if (V->Allocated) {
                    MDBFILEFreeValues(V);

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

BOOL MDBFILEFreeValue(unsigned long Index, MDBValueStruct *V)
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

static MDBFILESchemaAttribute *
MDBFILELoadSchemaAttribute(const unsigned char *Attribute)
{
    unsigned char *ptr;
    FILE *handle;
    struct stat buf;
    MDBFILESchemaAttribute *a = NULL;

    a = (MDBFILESchemaAttribute *)malloc(sizeof(MDBFILESchemaAttribute));
    if (a) {
        a->flags = 0;
        a->syntax = MDB_ATTR_SYN_STRING;
        a->asn1[0] = '\0';

        strcpy(a->name, Attribute);

        ptr = a->filename + MDBFILEAttributeToFilename(Attribute, a->filename);
        if (stat(a->filename, &buf) == 0) {
            strcpy(ptr, "/name");
            handle = fopen(a->filename, "rb");
            if (handle) {
                if (fgets(a->name, sizeof(a->name), handle)) {
                    ChopNL(a->name);
                }

                fclose(handle);
            }

            strcpy(ptr, "/flags");
            handle = fopen(a->filename, "rb");
            if (handle) {
                if (fgets(a->buffer, sizeof(a->buffer), handle)) {
                    a->flags = atol(a->buffer);
                }

                fclose(handle);
            }

            strcpy(ptr, "/syntax");
            handle = fopen(a->filename, "rb");
            if (handle) {
                if (fgets(a->buffer, sizeof(a->buffer), handle)) {
                    a->syntax = atol(a->buffer);
                }

                fclose(handle);
            }

            strcpy(ptr, "/asn1");
            handle = fopen(a->filename, "rb");
            if (handle) {
                fgets(a->asn1, sizeof(a->asn1), handle);

                fclose(handle);
            }
        }

        *ptr = '\0';
    }

    return(a);
}

static void 
MDBFILECreatePath(unsigned char *Filename)
{
    register unsigned char *ptr;
    register unsigned char *ptr2;
    struct stat buf;

    if (stat(Filename, &buf) != 0) {
        ptr = strchr(Filename, '/');

        while (ptr) {
            *ptr = '\0';

            if (stat(Filename, &buf) != 0) {
                XplMakeDir(Filename);
            }

            *ptr = '/';
            ptr2 = ptr;
            ptr = strchr(ptr2 + 1, '/');
        }
        
        if (stat(Filename, &buf) != 0) {
            XplMakeDir(Filename);
        }
    }

    return;
}

static BOOL 
MDBFILESaveSchemaAttribute(MDBFILESchemaAttribute *Attribute)
{
    unsigned char *ptr;
    FILE *handle;

    MDBFILECreatePath(Attribute->filename);

    ptr = Attribute->filename + strlen(Attribute->filename);

    strcpy(ptr, "/name");
    handle = fopen(Attribute->filename, "wb");
    if (handle && fprintf(handle, "%s\r\n", Attribute->name)) {
        fclose(handle);
    } else {
        if (handle) {
            fclose(handle);
        }

        unlink(Attribute->filename);
    }

    strcpy(ptr, "/flags");
    handle = fopen(Attribute->filename, "wb");
    if (handle && fprintf(handle, "%lu\r\n", Attribute->flags)) {
        fclose(handle);
    } else {
        if (handle) {
            fclose(handle);
        }

        unlink(Attribute->filename);
    }

    strcpy(ptr, "/syntax");
    handle = fopen(Attribute->filename, "wb");
    if (handle && fprintf(handle, "%lu\r\n", Attribute->syntax)) {
        fclose(handle);
    } else {
        if (handle) {
            fclose(handle);
        }

        unlink(Attribute->filename);
    }

    if (Attribute->asn1[0]) {
        strcpy(ptr, "/asn1");
        handle = fopen(Attribute->filename, "wb");
        if (handle && fprintf(handle, "%s\r\n", Attribute->asn1)) {
            fclose(handle);
        } else {
            if (handle) {
                fclose(handle);
            }

            unlink(Attribute->filename);
        }
    }

    *ptr = '\0';

    return(TRUE);
}

static void 
MDBFILEFreeSchemaAttribute(MDBFILESchemaAttribute *Attribute)
{
    if (Attribute) {
        free(Attribute);
    }

    return;
}

static void 
MDBFILEFreeSchemaClass(MDBFILESchemaClass *Class)
{
    if (Class) {
        if (Class->optional) {
            MDBFILEDestroyValueStruct(Class->optional);
        }

        if (Class->mandatory) {
            MDBFILEDestroyValueStruct(Class->mandatory);
        }

        if (Class->naming) {
            MDBFILEDestroyValueStruct(Class->naming);
        }

        if (Class->containedBy) {
            MDBFILEDestroyValueStruct(Class->containedBy);
        }

        if (Class->contains) {
            MDBFILEDestroyValueStruct(Class->contains);
        }

        if (Class->superClass) {
            MDBFILEDestroyValueStruct(Class->superClass);
        }

        if (Class->defaultACL) {
            MDBFILEDestroyValueStruct(Class->defaultACL);
        }

        free(Class);
    }

    return;
}

static MDBFILESchemaClass *
MDBFILELoadSchemaClass(const unsigned char *Class)
{
    unsigned char *ptr;
    FILE *handle;
    MDBFILESchemaClass *c = NULL;
    struct stat buf;

    c = (MDBFILESchemaClass *)malloc(sizeof(MDBFILESchemaClass));
    if (c) {
        c->flags = 0;
        c->asn1[0] = '\0';

        c->defaultACL = MDBFILECreateValueStruct(MDBFile.directoryHandle, NULL);
        if (c->defaultACL != NULL) {
            c->superClass = MDBFILEShareContext(c->defaultACL);
            c->containedBy = MDBFILEShareContext(c->defaultACL);
            c->contains = MDBFILEShareContext(c->defaultACL);
            c->naming = MDBFILEShareContext(c->defaultACL);
            c->mandatory = MDBFILEShareContext(c->defaultACL);
            c->optional = MDBFILEShareContext(c->defaultACL);

            if (c->superClass && c->containedBy && c->contains && c->naming && c->mandatory && c->optional) {
                strcpy(c->name, Class);
            } else {
                MDBFILEFreeSchemaClass(c);

                return(NULL);
            }
        } else {
            free(c);

            return(NULL);
        }

        ptr = c->filename + MDBFILEClassToFilename(Class, c->filename);
        if (stat(c->filename, &buf) == 0) {
            strcpy(ptr, "/name");
            handle = fopen(c->filename, "rb");
            if (handle) {
                if (fgets(c->name, sizeof(c->name), handle)) {
                    ChopNL(c->name);
                }

                fclose(handle);
            }

            strcpy(ptr, "/flags");
            handle = fopen(c->filename, "rb");
            if (handle) {
                if (fgets(c->buffer, sizeof(c->buffer), handle)) {
                    c->flags = atol(c->buffer);
                }

                fclose(handle);
            }

            strcpy(ptr, "/asn1");
            handle = fopen(c->filename, "rb");
            if (handle) {
                fgets(c->asn1, sizeof(c->asn1), handle);

                c->asn1[256] = '\0';

                fclose(handle);
            }
        } else {
            return(c);
        }

        strcpy(ptr, "/defaultacl");
        handle = fopen(c->filename, "rb");
        if (handle) {
            while (!feof(handle) && !ferror(handle)) {
                if (fgets(c->buffer, sizeof(c->buffer), handle)) {
                    ChopNL(c->buffer);

                    MDBFILEAddValue(c->buffer, c->defaultACL);
                }
            }

            fclose(handle);
        }

        strcpy(ptr, "/superclass");
        handle = fopen(c->filename, "rb");
        if (handle) {
            while (!feof(handle) && !ferror(handle)) {
                if (fgets(c->buffer, sizeof(c->buffer), handle)) {
                    ChopNL(c->buffer);

                    MDBFILEAddValue(c->buffer, c->superClass);
                }
            }

            fclose(handle);
        }

        strcpy(ptr, "/containedby");
        handle = fopen(c->filename, "rb");
        if (handle) {
            while (!feof(handle) && !ferror(handle)) {
                if (fgets(c->buffer, sizeof(c->buffer), handle)) {
                    ChopNL(c->buffer);

                    MDBFILEAddValue(c->buffer, c->containedBy);
                }
            }

            fclose(handle);
        }

        strcpy(ptr, "/contains");
        handle = fopen(c->filename, "rb");
        if (handle) {
            while (!feof(handle) && !ferror(handle)) {
                if (fgets(c->buffer, sizeof(c->buffer), handle)) {
                    ChopNL(c->buffer);

                    MDBFILEAddValue(c->buffer, c->contains);
                }
            }

            fclose(handle);
        }

        strcpy(ptr, "/naming");
        handle = fopen(c->filename, "rb");
        if (handle) {
            while (!feof(handle) && !ferror(handle)) {
                if (fgets(c->buffer, sizeof(c->buffer), handle)) {
                    ChopNL(c->buffer);

                    MDBFILEAddValue(c->buffer, c->naming);
                }
            }

            fclose(handle);
        }

        strcpy(ptr, "/mandatory");
        handle = fopen(c->filename, "rb");
        if (handle) {
            while (!feof(handle) && !ferror(handle)) {
                if (fgets(c->buffer, sizeof(c->buffer), handle)) {
                    ChopNL(c->buffer);

                    MDBFILEAddValue(c->buffer, c->mandatory);
                }
            }

            fclose(handle);
        }

        strcpy(ptr, "/optional");
        handle = fopen(c->filename, "rb");
        if (handle) {
            while (!feof(handle) && !ferror(handle)) {
                if (fgets(c->buffer, sizeof(c->buffer), handle)) {
                    ChopNL(c->buffer);

                    MDBFILEAddValue(c->buffer, c->optional);
                }
            }

            fclose(handle);
        }

        *ptr = '\0';
    }

    return(c);
}

static BOOL 
MDBFILESaveSchemaClass(MDBFILESchemaClass *Class)
{
    unsigned long i;
    unsigned char *ptr;
    FILE *handle;

    MDBFILECreatePath(Class->filename);

    ptr = Class->filename + strlen(Class->filename);

    strcpy(ptr, "/name");
    handle = fopen(Class->filename, "wb");
    if (handle) {
        fprintf(handle, "%s\r\n", Class->name);
        fclose(handle);
    }

    strcpy(ptr, "/flags");
    handle = fopen(Class->filename, "wb");
    if (handle) {
        fprintf(handle, "%lu\r\n", Class->flags);
        fclose(handle);
    }

    if (Class->asn1[0] != '\0') {
        strcpy(ptr, "/asn1");
        handle = fopen(Class->filename, "rb");
        if (handle) {
            fprintf(handle, "%s\r\n", Class->asn1);
            fclose(handle);
        }
    }

    if (Class->defaultACL->Used) {
        strcpy(ptr, "/defaultacl");
        handle = fopen(Class->filename, "wb");
        if (handle) {
            for (i = 0; i < Class->defaultACL->Used; i++) {
                fprintf(handle, "%s\r\n", Class->defaultACL->Value[i]);
            }

            fclose(handle);
        }
    }

    if (Class->superClass->Used) {
        strcpy(ptr, "/superclass");
        handle = fopen(Class->filename, "wb");
        if (handle) {
            for (i = 0; i < Class->superClass->Used; i++) {
                fprintf(handle, "%s\r\n", Class->superClass->Value[i]);
            }

            fclose(handle);
        }
    }

    if (Class->containedBy->Used) {
        strcpy(ptr, "/containedby");
        handle = fopen(Class->filename, "wb");
        if (handle) {
            for (i = 0; i < Class->containedBy->Used; i++) {
                fprintf(handle, "%s\r\n", Class->containedBy->Value[i]);
            }

            fclose(handle);
        }
    } else {
        LogMessage("[%04d] MDBSaveSchemaClass(\"%s\") has no containment.\r\n", __LINE__, Class->name);
    }

    if (Class->contains->Used) {
        strcpy(ptr, "/contains");
        handle = fopen(Class->filename, "wb");
        if (handle) {
            for (i = 0; i < Class->contains->Used; i++) {
                fprintf(handle, "%s\r\n", Class->contains->Value[i]);
            }

            fclose(handle);
        }
    }

    if (Class->naming->Used) {
        strcpy(ptr, "/naming");
        handle = fopen(Class->filename, "wb");
        if (handle) {
            for (i = 0; i < Class->naming->Used; i++) {
                fprintf(handle, "%s\r\n", Class->naming->Value[i]);
            }

            fclose(handle);
        }
    } else {
        LogMessage("[%04d] MDBSaveSchemaClass(\"%s\") has no naming.\r\n", __LINE__, Class->name);
    }

    if (Class->mandatory->Used) {
        strcpy(ptr, "/mandatory");
        handle = fopen(Class->filename, "wb");
        if (handle) {
            for (i = 0; i < Class->mandatory->Used; i++) {
                fprintf(handle, "%s\r\n", Class->mandatory->Value[i]);
            }

            fclose(handle);
        }
    }

    if (Class->optional->Used) {
        strcpy(ptr, "/optional");
        handle = fopen(Class->filename, "wb");
        if (handle) {
            for (i = 0; i < Class->optional->Used; i++) {
                fprintf(handle, "%s\r\n", Class->optional->Value[i]);
            }

            fclose(handle);
        }
    }

    *ptr = '\0';

    return(TRUE);
}

MDBHandle
MDBFILEAuthenticateFunction(const unsigned char *Object, const unsigned char *Password, const unsigned char *Arguments)
{
    FILE *handle;
    unsigned char *buffer;
    unsigned char objectClass[VALUE_BUFFER_SIZE];
    unsigned char filename[(XPL_MAX_PATH * 3) + 1];
    BOOL result = FALSE;
    struct stat sb;

    if (Object) {
        MDBFILEObjectToFilename(Object, "Private Key", filename, NULL, NULL);
        if (stat(filename, &sb) == 0) {
            buffer = (unsigned char *)malloc(sb.st_size + 1);
            if (buffer) {
                handle = fopen(filename, "rb");
                if (handle) {
                    if (fread(buffer, sizeof(unsigned char), sb.st_size, handle) == (size_t)sb.st_size) {
                        buffer[sb.st_size] = '\0';

                        if (strcmp(Password, DecodeBase64(buffer)) == 0) {
                            result = TRUE;
                        } else {
                            LogMessage("[%04d] MDBAuthenticate() password was not correct\r\n", __LINE__);
                        }
                    }

                    fclose(handle);
                }

                free(buffer);
            }
        } else if (!Password || (Password[0] == '\0')) {
            result = TRUE;
        } else {
            LogMessage("[%04d] MDBAuthenticate() could not access the password file and a password was specified.\r\n", __LINE__);
        }
    } else {
        result = TRUE;
    }

    if (result) {
        unsigned char *e;
        unsigned char classfile[(XPL_MAX_PATH * 3) + 1];
        BOOL readonly = TRUE;
        MDBFILESchemaClass *c = NULL;

        /*
            If the object authenticating is a user object, other than admin,
            then that user is read only.
        */

        /* Get the class name */
        MDBFILEObjectToFilename(Object, "Object Class", classfile, NULL, NULL);

        handle = fopen(classfile, "rb");
        if (handle) {
            while (!feof(handle) && !ferror(handle)) {
                if (fgets(objectClass, sizeof(objectClass), handle)) {
                    ChopNL(objectClass);

                    c = MDBFILELoadSchemaClass(objectClass);

                    break;
                }
            }

            fclose(handle);
        }

        if (Object) {
            /* fixme - Set readonly=FALSE; for bongomanagers as well */

            if ((c && (XplStrCaseCmp(c->name, "user") != 0)) || (XplStrCaseCmp(Object, "\\Tree\\Admin") == 0)) {
                readonly = FALSE;
            }

            /* Strip off the filename - we only need the dirname */
            e = strrchr(filename, '/');
            if (e) {
                *e = '\0';
            }

	    MDBFILEFreeSchemaClass(c);

            return((void *)MDBFileCreateContext(filename, readonly));
        }

	MDBFILEFreeSchemaClass(c);

        return((void *)MDBFileCreateContext("/", FALSE));
    }

    return(NULL);
}

BOOL 
MDBFILEDefineAttribute(const unsigned char *Attribute, const unsigned char *ASN1, unsigned long Type, BOOL SingleValue, BOOL ImmediateSync, BOOL Public, MDBValueStruct *V)
{
    BOOL result = FALSE;
    MDBFILESchemaAttribute *a;

    if (V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle) && !V->Handle->ReadOnly) {
        a = MDBFILELoadSchemaAttribute(Attribute);
        if (a) {
            if (SingleValue) {
                a->flags |= 0x00000001;
            } else {
                a->flags &= ~0x00000001;
            }

            if (ImmediateSync) {
                a->flags |= 0x00000040;
            } else {
                a->flags &= ~0x00000040;
            }

            if (Public) {
                a->flags |= 0x00000080;
            } else {
                a->flags &= ~0x00000080;
            }

            switch (Type) {
                case MDB_ATTR_SYN_BOOL: {
                    /* 'B'  */
                    a->syntax = 7;
                    break;
                }

                case MDB_ATTR_SYN_DIST_NAME: {
                    /* 'D'  */
                    a->syntax = 1;
                    break;
                }

                case MDB_ATTR_SYN_BINARY: {
                    /* 'O'  */
                    a->syntax = 9;
                    break;
                }

                case MDB_ATTR_SYN_STRING: {
                    /* 'S'  */
                    a->syntax = 3;
                    break;
                }

                default: {
                    /*  Unknown syntax  */
                    a->syntax = 0;
                    break;
                }
            }

            if (ASN1) {
                strcpy(a->asn1, ASN1);
            } else {
                a->asn1[0] = '\0';
            }

            result = MDBFILESaveSchemaAttribute(a);

            MDBFILEFreeSchemaAttribute(a);
        }
    }

    return(result);
}

BOOL 
MDBFILEAddAttribute(const unsigned char *Attribute, const unsigned char *Class, MDBValueStruct *V)
{
    unsigned long i;
    BOOL result = FALSE;
    MDBFILESchemaClass *c;

    if (V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle) && !V->Handle->ReadOnly) {
        c = MDBFILELoadSchemaClass(Class);
        if (c) {
            for (i = 0; i < c->optional->Used; i++) {
                if (strcmp(Attribute, c->optional->Value[i]) != 0) {
                    continue;
                }

                result = TRUE;
                break;
            }

            if (!result) {
                MDBFILEAddValue(Attribute, c->optional);

                result = MDBFILESaveSchemaClass(c);
            }

            MDBFILEFreeSchemaClass(c);
        }
    }

    return(result);
}

BOOL 
MDBFILEUndefineAttribute(const unsigned char *Attribute, MDBValueStruct *V)
{
    LogMessage("[%04d] MDBUndefineAttribute(\"%s\", ...) is not implemented\r\n", __LINE__, Attribute);

    return(FALSE);
}

static void 
MDBFILEInheritContainment(unsigned char *Superclass, MDBValueStruct *ContainedBy)
{
    unsigned long i;
    unsigned long j;
    BOOL found;
    MDBFILESchemaClass *s;

    s = MDBFILELoadSchemaClass(Superclass);
    if (s) {
        if (s->containedBy && s->containedBy->Used) {
            for (i = 0; i < s->containedBy->Used; i++) {
                found = FALSE;

                for (j = 0; j < ContainedBy->Used; j++) {
                    if (XplStrCaseCmp(s->containedBy->Value[i], ContainedBy->Value[j]) != 0) {
                        continue;
                    }

                    found = TRUE;
                    break;
                }

                if (!found) {
                    LogMessage("[%04d] MDBFILEInheritContainment(\"%s\", ...) inheriting \"%s\".\r\n", __LINE__, Superclass, s->containedBy->Value[i]);

                    MDBFILEAddValue(s->containedBy->Value[i], ContainedBy);
                }
            }
        } else if (s->superClass && s->superClass->Used) {
            MDBFILEInheritContainment(s->superClass->Value[0], ContainedBy);
        }

        MDBFILEFreeSchemaClass(s);
    }

    return;
}

static void 
MDBFILEInheritNaming(unsigned char *Superclass, MDBValueStruct *Naming)
{
    unsigned long i;
    unsigned long j;
    BOOL found;
    MDBFILESchemaClass *s;

    s = MDBFILELoadSchemaClass(Superclass);
    if (s) {
        if (s->naming && s->naming->Used) {
            for (i = 0; i < s->naming->Used; i++) {
                found = FALSE;

                for (j = 0; j < Naming->Used; j++) {
                    if (XplStrCaseCmp(s->naming->Value[i], Naming->Value[j]) != 0) {
                        continue;
                    }

                    found = TRUE;
                    break;
                }

                if (!found) {
                    LogMessage("[%04d] MDBFILEInheritNaming(\"%s\", ...) inheriting \"%s\".\r\n", __LINE__, Superclass, s->naming->Value[i]);

                    MDBFILEAddValue(s->naming->Value[i], Naming);
                }
            }
        } else if (s->superClass && s->superClass->Used) {
            MDBFILEInheritNaming(s->superClass->Value[0], Naming);
        }

        MDBFILEFreeSchemaClass(s);
    }

    return;
}

static void 
MDBFILEUpdateSchemaContainment(MDBFILESchemaClass *Class, MDBValueStruct *Superclass, MDBValueStruct *Containment)
{
    unsigned long i;
    unsigned long j;
    BOOL found;
    MDBFILESchemaClass *s;
    MDBFILESchemaClass *c;

    if (Containment && Containment->Used) {
        for (i = 0; i < Containment->Used; i++) {
            c = MDBFILELoadSchemaClass(Containment->Value[i]);
            if (c) {
                found = FALSE;

                for (j = 0; j < c->contains->Used; j++) {
                    if (XplStrCaseCmp(Class->name, c->contains->Value[j]) != 0) {
                        continue;
                    }

                    found = TRUE;
                    break;
                }

                if (!found) {
                    MDBFILEAddValue(Class->name, c->contains);

                    MDBFILESaveSchemaClass(c);
                }

                MDBFILEFreeSchemaClass(c);
            }
        }
    } else if (Superclass && Superclass->Used) {
        s = MDBFILELoadSchemaClass(Superclass->Value[0]);
        if (s) {
            MDBFILEUpdateSchemaContainment(Class, s->superClass, s->containedBy);

            MDBFILEFreeSchemaClass(s);
        }
    }

    return;
}

BOOL 
MDBFILEDefineClass(const unsigned char *Class, const unsigned char *ASN1, BOOL Container, MDBValueStruct *Superclass, MDBValueStruct *Containment, MDBValueStruct *Naming, MDBValueStruct *Mandatory, MDBValueStruct *Optional, MDBValueStruct *V)
{
    unsigned long i;
    unsigned long j;
    BOOL found;
    BOOL result = FALSE;
    MDBFILESchemaClass *c;

    if (V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle) && !V->Handle->ReadOnly) {
        c = MDBFILELoadSchemaClass(Class);
        if (c) {
            if (Container) {
                c->flags |= 0x00000003;
            } else {
                c->flags &= ~0x00000001;
            }

            if (Superclass) {
                for (i = 0; i < Superclass->Used; i++) {
                    found = FALSE;

                    for (j = 0; j < c->superClass->Used; j++) {
                        if (XplStrCaseCmp(Superclass->Value[i], c->superClass->Value[j]) != 0) {
                            continue;
                        }

                        found = TRUE;
                        break;
                    }

                    if (!found) {
                        MDBFILEAddValue(Superclass->Value[i], c->superClass);
                    }
                }
            }

            if (Containment && Containment->Used) {
                for (i = 0; i < Containment->Used; i++) {
                    found = FALSE;

                    for (j = 0; j < c->containedBy->Used; j++) {
                        if (XplStrCaseCmp(Containment->Value[i], c->containedBy->Value[j]) != 0) {
                            continue;
                        }

                        found = TRUE;
                        break;
                    }

                    if (!found) {
                        MDBFILEAddValue(Containment->Value[i], c->containedBy);
                    }
                }
            } else if (c->superClass->Used) {
                MDBFILEInheritContainment(c->superClass->Value[0], c->containedBy);
            }

            if (Naming && Naming->Used) {
                for (i = 0; i < Naming->Used; i++) {
                    found = FALSE;

                    for (j = 0; j < c->naming->Used; j++) {
                        if (XplStrCaseCmp(Naming->Value[i], c->naming->Value[j]) != 0) {
                            continue;
                        }

                        found = TRUE;
                        break;
                    }

                    if (!found) {
                        MDBFILEAddValue(Naming->Value[i], c->naming);
                    }
                }
            } else if (c->superClass->Used) {
                MDBFILEInheritNaming(c->superClass->Value[0], c->naming);
            }

            if (Mandatory) {
                for (i = 0; i < Mandatory->Used; i++) {
                    found = FALSE;

                    for (j = 0; j < c->mandatory->Used; j++) {
                        if (XplStrCaseCmp(Mandatory->Value[i], c->mandatory->Value[j]) != 0) {
                            continue;
                        }

                        found = TRUE;
                        break;
                    }

                    if (!found) {
                        MDBFILEAddValue(Mandatory->Value[i], c->mandatory);
                    }
                }
            }

            if (Optional) {
                for (i = 0; i < Optional->Used; i++) {
                    found = FALSE;

                    for (j = 0; j < c->optional->Used; j++) {
                        if (XplStrCaseCmp(Optional->Value[i], c->optional->Value[j]) != 0) {
                            continue;
                        }

                        found = TRUE;
                        break;
                    }

                    if (!found) {
                        MDBFILEAddValue(Optional->Value[i], c->optional);
                    }
                }
            }

            result = MDBFILESaveSchemaClass(c);
            if (result) {
                MDBFILEUpdateSchemaContainment(c, Superclass, Containment);
            }

            MDBFILEFreeSchemaClass(c);
        }
    }

    return(result);
}

BOOL 
MDBFILEUndefineClass(const unsigned char *Class, MDBValueStruct *V)
{
    LogMessage("[%04d] MDBUndefineClass(\"%s\", ...) is not implemented\r\n", __LINE__, Class);

    return(FALSE);
}

BOOL 
MDBFILEListContainableClasses(const unsigned char *Object, MDBValueStruct *V)
{
    unsigned long i;
    FILE *handle;
    MDBFILESchemaClass *c;

    if (V && MDB_CONTEXT_IS_VALID(V)) {
        V->Buffer[0] = '\0';

        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, "Object Class", V->Filename, V, NULL);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, "Object Class", V->Filename, V, NULL);
        }

        handle = fopen(V->Filename, "rb");
        if (handle) {
            if (fgets(V->Buffer, sizeof(V->Buffer), handle)) {
                ChopNL(V->Buffer);
            }

            fclose(handle);
        }

        if (V->Buffer[0]) {
            c = MDBFILELoadSchemaClass(V->Buffer);
            if (c) {
                for (i = 0; i < c->contains->Used; i++) {
                    MDBFILEAddValue(c->contains->Value[i], V);
                }

                MDBFILEFreeSchemaClass(c);
            }

            return(TRUE);
        }
    }

    return(FALSE);
}

const unsigned char *
MDBFILEListContainableClassesEx(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V)
{
    unsigned char *ptr;
    MDBEnumStruct *es = E;

    if (es) {
        if (!es->Initialized) {
            es->File = NULL;
            es->EntriesLeft = 0;

            if (V && MDB_CONTEXT_IS_VALID(V)) {
                V->Buffer[0] = '\0';

                if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
                    MDBFILEObjectToFilename(Object, "Object Class", V->Filename, V, NULL);
                } else {
                    MDBFILEObjectToFilename(V->BaseDN, "Object Class", V->Filename, V, NULL);
                }

                es->File = fopen(V->Filename, "rb");
                if (es->File) {
                    if (fgets(V->Buffer, sizeof(V->Buffer), es->File)) {
                        ChopNL(V->Buffer);
                    }

                    fclose(es->File);
                    es->File = NULL;
                }

                if (V->Buffer) {
                    ptr = V->Filename + MDBFILEClassToFilename(V->Buffer, V->Filename);

                    strcpy(ptr, "/contains");
                    es->File = fopen(V->Filename, "rb");
                    if (es->File) {
                        es->Initialized = TRUE;
                        es->EntriesLeft = 1;
                    }
                }
            }
        }

        if (es->EntriesLeft) {
            if (!feof(es->File) && !ferror(es->File)) {
                if (fgets(es->Buffer, sizeof(es->Buffer), es->File)) {
                    LogMessage("[%04d] MDBFILEListContainableClassesEx(\"%s\", ...) returning \"%s\".\r\n", __LINE__, Object, es->Buffer);

                    return(es->Buffer);
                }
            }
        }

        es->EntriesLeft = 0;

        if (es->File != NULL) {
            fclose(es->File);
            es->File = NULL;
        }

        es->Initialized = FALSE;
    }

    return(NULL);
}

long 
MDBFILERead(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
    int offset;
    unsigned long count;
    FILE *handle;
    MDBFILESchemaAttribute *a;

    if (Attribute && V && MDB_CONTEXT_IS_VALID(V)) {
        a = MDBFILELoadSchemaAttribute(Attribute);
        if (a) {
            if (a->syntax != 1) {
                offset = 0;
            } else {
                offset = strlen(V->BaseDN) + 1;
            }

            MDBFILEFreeSchemaAttribute(a);
            a = NULL;

            count = V->Used;
        } else {
            return(0);
        }

        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, Attribute, V->Filename, V, NULL);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, Attribute, V->Filename, V, NULL);
        }

        handle = fopen(V->Filename, "rb");
        if (handle) {
            while (!feof(handle) && !ferror(handle)) {
                if (fgets(V->Buffer, sizeof(V->Buffer), handle)) {
                    ChopNL(V->Buffer);

                    /* unescape the attribute value */
                    if (offset == 0) {
                        unsigned char *ptr = strchr(V->Buffer, '\\');
                        while (ptr != NULL) {
                            if (*(ptr+1) == 'r') {
                                strcpy(ptr, ptr+1);
                                *ptr = '\r';
                            } else if (*(ptr+1) == 'n') {
                                strcpy(ptr, ptr+1);
                                *ptr = '\n';
                            } else if (*(ptr+1) == '\\') {
                                strcpy(ptr, ptr+1);
                            }
                            ptr = strchr(ptr + 1, '\\');
                        }
                    }

                    LogMessage("[%04d] MDBRead(\"%s\", \"%s\", ...) adding \"%s\".\r\n", __LINE__, Object, Attribute, V->Buffer + offset);

                    MDBFILEAddValue(V->Buffer + offset, V);
                }
            }

            fclose(handle);
        } else {
            LogMessage("[%04d] MDBRead(\"%s\", \"%s\", ...) failed to open \"%s\"; error %d\r\n", __LINE__, Object, Attribute, V->Filename, errno);
        }

        return(V->Used - count);
    }

    return(0);
}

const unsigned char *
MDBFILEReadEx(const unsigned char *Object, const unsigned char *Attribute, MDBEnumStruct *E, MDBValueStruct *V)
{
    MDBFILESchemaAttribute *a;

    if (E) {
        if (!E->Initialized) {
            E->File = NULL;
            E->EntriesLeft = 0;

            if (Attribute && V && MDB_CONTEXT_IS_VALID(V)) {
                a = MDBFILELoadSchemaAttribute(Attribute);
                if (a) {
                    if (a->syntax != 1) {
                        E->Offset = 0;
                    } else {
                        E->Offset = strlen(V->BaseDN) + 1;
                    }

                    MDBFILEFreeSchemaAttribute(a);
                    a = NULL;

                    if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
                        MDBFILEObjectToFilename(Object, Attribute, V->Filename, V, NULL);
                    } else {
                        MDBFILEObjectToFilename(V->BaseDN, Attribute, V->Filename, V, NULL);
                    }

                    E->File = fopen(V->Filename, "rb");
                    if (E->File) {
                        E->Initialized = TRUE;
                        E->EntriesLeft = 1;
                    } else {
                        LogMessage("[%04d] MDBReadEx(\"%s\", \"%s\", ...) failed to open \"%s\"; error %d\r\n", __LINE__, Object, Attribute, V->Filename, errno);
                    }
                }
            }
        }

        if (E->EntriesLeft) {
            if (!feof(E->File) && !ferror(E->File)) {
                if (fgets(E->Buffer, sizeof(E->Buffer), E->File)) {
                    /* unescape the attribute value */
                    if (E->Offset == 0) {
                        unsigned char *ptr = strchr(E->Buffer, '\\');
                        while (ptr != NULL) {
                            if (*(ptr+1) == 'r') {
                                strcpy(ptr, ptr+1);
                                *ptr = '\r';
                            } else if (*(ptr+1) == 'n') {
                                strcpy(ptr, ptr+1);
                                *ptr = '\n';
                            } else if (*(ptr+1) == '\\') {
                                strcpy(ptr, ptr+1);
                            }
                            ptr = strchr(ptr + 1, '\\');
                        }
                    }

                    LogMessage("[%04d] MDBReadEx(\"%s\", \"%s\", ...) returning \"%s\".\r\n", __LINE__, Object, Attribute, E->Buffer + E->Offset);

                    return(E->Buffer + E->Offset);
                }
            }
        }

        E->EntriesLeft = 0;

        if (E->File != NULL) {
            fclose(E->File);
            E->File = NULL;
        }

        E->Initialized = FALSE;
    }

    return(NULL);
}

/*
    The MDBFILEReadDN will always return a full DN, instead of a DN relative to the selected context
*/
long 
MDBFILEReadDN(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
    unsigned long count;
    FILE *handle;
    
    if (Attribute && V && MDB_CONTEXT_IS_VALID(V)) {
        count = V->Used;

        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, Attribute, V->Filename, V, NULL);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, Attribute, V->Filename, V, NULL);
        }

        handle = fopen(V->Filename, "rb");
        if (handle) {
            while (!feof(handle) && !ferror(handle)) {
                if (fgets(V->Buffer, sizeof(V->Buffer), handle)) {
                    ChopNL(V->Buffer);

                    LogMessage("[%04d] MDBReadDN(\"%s\", \"%s\", ...) adding \"%s\".\r\n", __LINE__, Object, Attribute, V->Buffer);

                    MDBFILEAddValue(V->Buffer, V);
                }
            }

            fclose(handle);
        } else {
            LogMessage("[%04d] MDBReadDN(\"%s\", \"%s\", ...) failed to open \"%s\"; error %d\r\n", __LINE__, Object, Attribute, V->Filename, errno);
        }

        return(V->Used - count);
    }

    return(0);
}

BOOL 
MDBFILEOpenStream(const unsigned char *Object, const unsigned char *AttributeIn, MDBValueStruct *V)
{
    LogMessage("[%04d] MDBOpenStream(...) is not implemented\r\n", __LINE__);

    return(FALSE);
}

BOOL 
MDBFILEReadStream(MDBValueStruct *V)
{
    LogMessage("[%04d] MDBReadStream(...) is not implemented\r\n", __LINE__);

    return(FALSE);
}

BOOL 
MDBFILEWriteStream(MDBValueStruct *V)
{
    LogMessage("[%04d] MDBWriteStream(...) is not implemented\r\n", __LINE__);

    return(FALSE);
}

BOOL 
MDBFILECloseStream(MDBValueStruct *V)
{
    LogMessage("[%04d] MDBCloseStream(...) is not implemented\r\n", __LINE__);

    return(FALSE);
}

BOOL 
MDBFILEWriteTyped(const unsigned char *Object, const unsigned char *Attribute, const int AttrType, MDBValueStruct *V)
{
    unsigned long i;
    unsigned char *ptr;
    BOOL result = FALSE;
    FILE *handle;

    if (Attribute && V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle)) {
        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, Attribute, V->Filename, V, &ptr);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, Attribute, V->Filename, V, &ptr);
        }

        /* Allow a readonly handle to write to the object that opened the handle */
        if (MDBFileIsReadWrite(V)) {
        *ptr = '\0';
        MDBFILECreatePath(V->Filename);
        *ptr++ = '/';

        handle = fopen(V->Filename, "wb");
        if (handle) {
            result = TRUE;

            for (i = 0; i < V->Used; i++) {
                switch(AttrType) {
                    case MDB_ATTR_SYN_DIST_NAME: {
                        if (V->Value[i][0] != '\\') {
                            sprintf(V->Buffer, "%s\\%s", V->BaseDN, V->Value[i]);
                        } else {
                            strcpy(V->Buffer, V->Value[i]);
                        }

                        LogMessage("[%04d] MDBWriteDN(\"%s\", \"%s\", V->Value[%d] = \"%s\")\r\n", __LINE__, Object, Attribute, i, V->Buffer);

                        fprintf(handle, "%s\r\n", V->Buffer);

                        break;
                    }

                    case MDB_ATTR_SYN_STRING: 
                    default: {
                        unsigned char *special;

                        LogMessage("[%04d] MDBWrite(\"%s\", \"%s\", V->Value[%d] = \"%s\")\r\n", __LINE__, Object, Attribute, i, V->Value[i]);

                        /* output the attribute value and escape CR, LF & \ as \r, \n & \\ respecitively */
                        ptr = V->Value[i];
                        while ((special = strpbrk(ptr, "\n\r\\")) != NULL) {
                            fwrite(ptr, sizeof(unsigned char), special-ptr, handle);
                            fputc('\\', handle);
                            if (*special == '\r') {
                                fputc('r', handle);
                            } else if (*special == '\n') {
                                fputc('n', handle);
                            } else {
                                fputc(*special, handle);
                            }
                            ptr = special + 1;
                        }
                        fprintf(handle, "%s\r\n", ptr);
                        break;
                    }
                }
            }

            fclose(handle);
        } else {
            LogMessage("[%04d] MDBFILEWriteTyped(\"%s\", \"%s\", ...) failed to create \"%s\"; error %d\r\n", __LINE__, Object, Attribute, V->Filename, errno);
        }
    }
    }

    return(result);
}

BOOL 
MDBFILEWrite(const unsigned char *Object, const unsigned char *AttributeIn, MDBValueStruct *V)
{
    return(MDBFILEWriteTyped(Object, AttributeIn, MDB_ATTR_SYN_STRING, V));
}

BOOL 
MDBFILEWriteDN(const unsigned char *Object, const unsigned char *AttributeIn, MDBValueStruct *V)
{
    return(MDBFILEWriteTyped(Object, AttributeIn, MDB_ATTR_SYN_DIST_NAME, V));
}

BOOL 
MDBFILEAdd(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
    unsigned char *ptr;
    FILE *handle;
    BOOL result = FALSE;
        
    if (Attribute && Value && V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle)) {
        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, Attribute, V->Filename, V, &ptr);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, Attribute, V->Filename, V, &ptr);
        }

        /* Allow a readonly handle to write to the object that opened the handle */
        if (MDBFileIsReadWrite(V)) {
        *ptr = '\0';
        MDBFILECreatePath(V->Filename);
        *ptr = '/';

        handle = fopen(V->Filename, "ab");
        if (handle) {
            LogMessage("[%04d] MDBAdd(\"%s\", \"%s\", \"%s\")\r\n", __LINE__, Object, Attribute, Value);

            fprintf(handle, "%s\r\n", Value);

            fclose(handle);

            result = TRUE;
        } else {
            LogMessage("[%04d] MDBAdd(\"%s\", \"%s\", ...) failed to append \"%s\" to \"%s\"; error %d\r\n", __LINE__, Object, Attribute, Value, V->Filename, errno);
        }
    }
    }

    return(result);
}

BOOL 
MDBFILEAddDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
    unsigned char *ptr;
    FILE *handle;
    BOOL result = FALSE;

    if (Attribute && V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle)) {
        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, Attribute, V->Filename, V, &ptr);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, Attribute, V->Filename, V, &ptr);
        }

        /* Allow a readonly handle to write to the object that opened the handle */
        if (MDBFileIsReadWrite(V)) {
        *ptr = '\0';
        MDBFILECreatePath(V->Filename);
        *ptr = '/';

        handle = fopen(V->Filename, "ab");
        if (handle) {
            result = TRUE;

            if (Value[0] != '\\') {
                sprintf(V->Buffer, "%s\\%s", V->BaseDN, Value);
            } else {
                strcpy(V->Buffer, Value);
            }

            LogMessage("[%04d] MDBAddDN(\"%s\", \"%s\", \"%s\")\r\n", __LINE__, Object, Attribute, V->Buffer);

            fprintf(handle, "%s\r\n", V->Buffer);

            fclose(handle);
        } else {
            LogMessage("[%04d] MDBAdd(\"%s\", \"%s\", ...) failed to append \"%s\" to \"%s\"; error %d\r\n", __LINE__, Object, Attribute, Value, V->Filename, errno);
        }
    }
    }

    return(result);
}

BOOL 
MDBFILERemove(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
    unsigned long i;
    unsigned char *ptr;
    unsigned char *special;
    MDBValueStruct *data;
    FILE *handle;
    struct stat buf;
    BOOL result = FALSE;

    if (V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle)) {
        data = MDBFILEShareContext(V);
        if (data) {
            if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
                MDBFILEObjectToFilename(Object, Attribute, V->Filename, V, &ptr);
            } else {
                MDBFILEObjectToFilename(V->BaseDN, Attribute, V->Filename, V, &ptr);
            }

            /* Allow a readonly handle to write to the object that opened the handle */
            if (MDBFileIsReadWrite(V)) {
                *ptr = '\0';
                if (stat(V->Filename, &buf) == 0) {
                    *ptr = '/';

                    handle = fopen(V->Filename, "rb");
                    if (handle) {
                        const unsigned char *ptrc;
                        while (!feof(handle) && !ferror(handle)) {
                            if (fgets(V->Buffer, sizeof(V->Buffer), handle)) {
                                ChopNL(V->Buffer);

                                MDBFILEAddValue(V->Buffer, data);
                            }
                        }

                        fclose(handle);

                        result = FALSE;

                        /* escape the attribute value so it's the same as in the file */
                        ptrc = Value;
                        V->Buffer[0] = 0;
                        while ((special = strpbrk(ptrc, "\n\r\\")) != NULL) {
                            strncat(V->Buffer, ptrc, special-ptrc);
                            if (*special == '\r') {
                                strcat(V->Buffer, "\\r");
                            } else if (*special == '\n') {
                                strcat(V->Buffer, "\\n");
                            } else {
                                strcat(V->Buffer, "\\\\");
                            }
                            ptrc = special + 1;
                        }
                        strcat(V->Buffer, ptrc);

                        for (i = 0; i < data->Used; ) {
                            if (XplStrCaseCmp(V->Buffer, data->Value[i]) != 0) {
                                i++;
                                continue;
                            }

                            MDBFILEFreeValue(i, data);

                            result = TRUE;
                        }

                        if (result) {
                            LogMessage("[%04d] MDBRemove(\"%s\", \"%s\", \"%s\")\r\n", __LINE__, Object, Attribute, Value);

                            if (data->Used) {
                                handle = fopen(V->Filename, "wb");
                                if (handle) {
                                    for (i = 0; i < data->Used; i++) {
                                        fprintf(handle, "%s\r\n", data->Value[i]);
                                    }

                                    fclose(handle);
                                } else {
                                    V->ErrNo = ERR_TRANSPORT_FAILURE;

                                    result = FALSE;
                                }
                            } else {
                                unlink(V->Filename);
                            }
                        } else {
                            V->ErrNo = ERR_NO_SUCH_VALUE;

                            result = TRUE;
                        }

                        MDBFILEDestroyValueStruct(data);
                    }
                } else {
                    V->ErrNo = ERR_NO_SUCH_ENTRY;
                }
            }
        }
    }

    return(result);
}

BOOL 
MDBFILERemoveDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V)
{
    unsigned long i;
    unsigned char *ptr;
    MDBValueStruct *data;
    FILE *handle;
    struct stat buf;
    BOOL result = FALSE;

    if (Attribute && V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle)) {
        data = MDBFILEShareContext(V);
        if (data) {
            if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
                MDBFILEObjectToFilename(Object, Attribute, V->Filename, V, &ptr);
            } else {
                MDBFILEObjectToFilename(V->BaseDN, Attribute, V->Filename, V, &ptr);
            }

            /* Allow a readonly handle to write to the object that opened the handle */
            if (MDBFileIsReadWrite(V)) {
            *ptr = '\0';
            if (stat(V->Filename, &buf) == 0) {
                *ptr = '/';

                handle = fopen(V->Filename, "rb");
                if (handle) {
                    while (!feof(handle) && !ferror(handle)) {
                        if (fgets(V->Buffer, sizeof(V->Buffer), handle)) {
                            ChopNL(V->Buffer);

                            MDBFILEAddValue(V->Buffer, data);
                        }
                    }

                    fclose(handle);

                    if (Value[0] != '\\') {
                        sprintf(V->Buffer, "%s\\%s", V->BaseDN, Value);
                    } else {
                        strcpy(V->Buffer, Value);
                    }

                    result = FALSE;
                    for (i = 0; i < data->Used; ) {
                        if (XplStrCaseCmp(V->Buffer, data->Value[i]) != 0) {
                            i++;
                            continue;
                        }

                        MDBFILEFreeValue(i, data);

                        result = TRUE;
                    }

                    if (result) {
                        LogMessage("[%04d] MDBRemoveDN(\"%s\", \"%s\", \"%s\")\r\n", __LINE__, Object, Attribute, V->Buffer);

                        if (data->Used) {
                            handle = fopen(V->Filename, "wb");
                            if (handle) {
                                for (i = 0; i < data->Used; i++) {
                                    fprintf(handle, "%s\r\n", data->Value[i]);
                                }

                                fclose(handle);
                            } else {
                                V->ErrNo = ERR_TRANSPORT_FAILURE;

                                result = FALSE;
                            }
                        } else {
                            unlink(V->Filename);
                        }
                    } else {
                        V->ErrNo = ERR_NO_SUCH_VALUE;

                        result = TRUE;
                    }

                    MDBFILEDestroyValueStruct(data);
                }
            } else {
                V->ErrNo = ERR_NO_SUCH_ENTRY;
            }
        }
    }
    }

    return(result);
}

BOOL 
MDBFILEClear(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V)
{
    unsigned char *ptr;
    
    if (Attribute && V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle)) {
        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, Attribute, V->Filename, V, &ptr);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, Attribute, V->Filename, V, &ptr);
        }

        /* Allow a readonly handle to write to the object that opened the handle */
        if (MDBFileIsReadWrite(V)) {
        *ptr = '\0';
        MDBFILECreatePath(V->Filename);
        *ptr = '/';

        unlink(V->Filename);

        return(TRUE);
    }
    }

    return(FALSE);
}

BOOL 
MDBFILEIsObject(const unsigned char *Object, MDBValueStruct *V)
{
    struct stat buf;
    if (V && MDB_CONTEXT_IS_VALID(V)) {
        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, NULL, V->Filename, V, NULL);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, NULL, V->Filename, V, NULL);
        }

        if (stat(V->Filename, &buf) == 0) {
            LogMessage("[%04d] MDBIsObject(\"%s\"...) TRUE.\r\n", __LINE__, Object);

            return(TRUE);
        }

        LogMessage("[%04d] MDBIsObject(\"%s\", ...) FALSE.\r\n", __LINE__, Object);
    }

    return(FALSE);
}

static BOOL
GetAliasedObject(const unsigned char *Object, MDBValueStruct *V)
{
    unsigned char *ptr;
    FILE *handle;
    BOOL result = FALSE;

    MDBFILEObjectToFilename(Object, "Aliased Object Name", V->Filename, V, &ptr);
    handle = fopen(V->Filename, "rb");
    if (handle) {
        while (!feof(handle) && !ferror(handle)) {
            if (fgets(V->Buffer, sizeof(V->Buffer), handle)) {
                ChopNL(V->Buffer);
                result = TRUE;
                break;
            }
        }

        fclose(handle);
    }

    return(result);
}

static MDBFILESchemaClass *
GetObjectClass(const unsigned char *Object, MDBValueStruct *V)
{
    unsigned char *ptr;
    FILE *handle;
    MDBFILESchemaClass *c = NULL;

    if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
        MDBFILEObjectToFilename(Object, "Object Class", V->Filename, V, &ptr);
    } else {
        MDBFILEObjectToFilename(V->BaseDN, "Object Class", V->Filename, V, &ptr);
    }

    handle = fopen(V->Filename, "rb");
    if (handle) {
        while (!feof(handle) && !ferror(handle)) {
            if (fgets(V->Buffer, sizeof(V->Buffer), handle)) {
                ChopNL(V->Buffer);

                c = MDBFILELoadSchemaClass(V->Buffer);
                break;
            }
        }

        fclose(handle);
    }

    return c;
}

__inline static BOOL
FreeSchemaClass(MDBFILESchemaClass *class)
{
    MDBFILEFreeSchemaClass(class);
    return(TRUE);
}

BOOL 
MDBFILEGetObjectDetails(const unsigned char *Object, unsigned char *Type, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V)
{
    BOOL result = FALSE;
    MDBValueStruct *Types = MDBFILEShareContext(V);

    if (Types) {
        result = MDBFILEGetObjectDetailsEx(Object, Types, RDN, DN, V);

        if (result && Types->Used > 0) {
            strcpy((char *) Type, (char *) Types->Value[0]);
        }

        MDBFILEDestroyValueStruct(Types);
    }

    return result;
}

BOOL 
MDBFILEGetObjectDetailsEx(const unsigned char *Object, MDBValueStruct *Types, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V)
{
    unsigned char *ptr;
    MDBFILESchemaClass *objectClass = NULL;
    BOOL result = TRUE;

    if (V && MDB_CONTEXT_IS_VALID(V)) {
        objectClass = GetObjectClass(Object, V);
        if (objectClass && 
            ((strcmp(objectClass->name, "Alias") != 0) || 
             ((GetAliasedObject(Object, V)) && 
              FreeSchemaClass(objectClass)  &&
              ((objectClass = GetObjectClass(V->Buffer, V)) != NULL)))) {


            /* GetObjectClass() leaves the path to the 'object class' file in V->Filename.  */
            /* we need to chop the filename off so we can get just the object path. */
            ptr = strrchr(V->Filename, '/');
            if (ptr) {
                *ptr = '\0';
            }

            MDBFILEFilenameToObjectDN(V->Filename, V->Buffer, &ptr);

            if (DN) {
                strcpy(DN, V->Buffer);
            }

            if (RDN) {
                strcpy(RDN, ptr);
            }

            if (Types) {
                result = MDBFILEAddValue(objectClass->name, Types);
            }


            LogMessage("[%04d] MDBGetObjectDetails(\"%s\", \"%s\", \"%s\", \"%s\", ...) success.\r\n", __LINE__, Object, objectClass->name, ptr, V->Buffer);

            MDBFILEFreeSchemaClass(objectClass);

            return(result);

        }
    }

    LogMessage("[%04d] MDBGetObjectDetails(\"%s\", ...) failed.\r\n", __LINE__, Object);

    return(FALSE);
}

BOOL 
MDBFILEVerifyPassword(const unsigned char *Object, const unsigned char *Password, MDBValueStruct *V)
{
    unsigned char *buffer;
    BOOL result = FALSE;
    FILE *handle;
    struct stat sb;

    if (V && MDB_CONTEXT_IS_VALID(V)) {
        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, "Private Key", V->Filename, V, NULL);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, "Private Key", V->Filename, V, NULL);
        }

        if (stat(V->Filename, &sb) == 0) {
            buffer = (unsigned char *)malloc(sb.st_size + 1);
            if (buffer) {
                handle = fopen(V->Filename, "rb");
                if (handle) {
                    if (fread(buffer, sizeof(unsigned char), sb.st_size, handle) == (size_t)sb.st_size) {
                        buffer[sb.st_size] = '\0';

                        if (strcmp(Password, DecodeBase64(buffer)) == 0) {
                            result = TRUE;
                        } else {
                            LogMessage("[%04d] MDBVerifyPassword() password was not correct\r\n", __LINE__);
                        }
                    }

                    fclose(handle);
                } else {
                    LogMessage("[%04d] MDBVerifyPassword() Could not open the password file");
                }

                free(buffer);
            }
        } else if (!Password || (Password[0] == '\0')) {
            result = TRUE;
        } else {
            LogMessage("[%04d] MDBVerifyPassword() could not access the password file and a password was specified.\r\n", __LINE__);
        }

    } else {
        LogMessage("[%04d] MDBVerifyPassword() context was not valid\r\n", __LINE__);
    }

    return(result);
}

BOOL 
MDBFILEChangePassword(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V)
{
    unsigned char *buffer;
    unsigned char *encoded;
    BOOL result = FALSE;
    FILE *handle;
    struct stat sb;

    if (V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle)) {
        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, "Private Key", V->Filename, V, NULL);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, "Private Key", V->Filename, V, NULL);
        }

        /* Allow a readonly handle to write to the object that opened the handle */
        if (MDBFileIsReadWrite(V)) {
        if (stat(V->Filename, &sb) == 0) {
            buffer = (unsigned char *)malloc(sb.st_size + 1);
            if (buffer) {
                handle = fopen(V->Filename, "rb");
                if (handle) {
                    if (fread(buffer, sizeof(unsigned char), sb.st_size, handle) == (size_t)sb.st_size) {
                        buffer[sb.st_size] = '\0';

                        if (strcmp(OldPassword, DecodeBase64(buffer)) == 0) {
                            result = TRUE;
                        }
                    }

                    fclose(handle);
                }

                free(buffer);
            }
        } else {
            result = TRUE;
        }

        if (result) {
            result = FALSE;

            handle = fopen(V->Filename, "wb");
            if (handle) {
                encoded = EncodeBase64(NewPassword);
                if (encoded) {
                    fwrite(encoded, sizeof(unsigned char), strlen(encoded), handle);

                    MemFree(encoded);

                    result = TRUE;
                }

                fclose(handle);
            }
        }
    }
    }

    return(result);
}

BOOL 
MDBFILEChangePasswordEx(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V)
{
    unsigned char *encoded;
    BOOL result = FALSE;
    FILE *handle;

    if (V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle)) {
        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, "Private Key", V->Filename, V, NULL);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, "Private Key", V->Filename, V, NULL);
        }

        /* Allow a readonly handle to write to the object that opened the handle */
        if (MDBFileIsReadWrite(V)) {
        handle = fopen(V->Filename, "wb");
        if (handle) {
            encoded = EncodeBase64(NewPassword);
            if (encoded) {
                fwrite(encoded, sizeof(unsigned char), strlen(encoded), handle);

                MemFree(encoded);

                result = TRUE;
            }

            fclose(handle);
        }
    }
    }

    return(result);
}

static BOOL
MDBFILERecurseAttributes(unsigned char *Path,  MDBValueStruct *V)
{
    unsigned char *delim;
    BOOL result = TRUE;
    XplDir *parent;
    XplDir *entry;
                                                                                                                                          
    delim = Path + strlen(Path);
                                                                                                                                          
    parent = XplOpenDir(Path);
    if (parent) {
        while ((entry = XplReadDir(parent)) != NULL) {
		// filter out the object entries (directories)
		if (!(entry->d_attr & XPL_A_SUBDIR) && (entry->d_name[0] != '.')) {
		    delim[0] = '/';
                    strcpy(delim + 1, entry->d_name);
		
		        MDBFILEFilenameToAttributeName(entry->d_name, V->Buffer);

			MDBFILEAddValue(V->Buffer, V);

                    continue;
	   } 
	
        }
                                                                                                                                          
        XplCloseDir(parent);
    } else {
        result = FALSE;
    }
                                                                                                                                          
    return(result);
}

static BOOL 
MDBFILERecurseObjects(unsigned char *Path, const unsigned char *Type, const unsigned char *Pattern, unsigned long Flags, MDBValueStruct *V)
{
    unsigned int offset = strlen(V->BaseDN) + 1;
    unsigned char *ptr;
    unsigned char *delim;
    FILE *handle;
    BOOL result = TRUE;
    XplDir *parent;
    XplDir *entry;

    delim = Path + strlen(Path);

    parent = XplOpenDir(Path);
    if (parent) {
        while ((entry = XplReadDir(parent)) != NULL) {
            if ((entry->d_attr & XPL_A_SUBDIR) && (entry->d_name[0] != '.')) {
                if (!Type) {
                    delim[0] = '/';
                    strcpy(delim + 1, entry->d_name);

                    MDBFILEFilenameToObjectDN(Path, V->Buffer, NULL);

                    MDBFILEAddValue(V->Buffer + offset, V);

                    continue;
                } else {
                    ptr = delim + sprintf(delim, "/%s/object%%032class", entry->d_name) - 16;

                    handle = fopen(Path, "rb");

                    *ptr = '\0';

                    if (handle) {
                        if (fgets(V->Buffer, sizeof(V->Buffer), handle)) {
                            ChopNL(V->Buffer);
                        } else {
                            LogMessage("[%04d] MDBFILERecurseObjects(...) read \"%s\" failed; error %d.\r\n", __LINE__, Path, errno);

                            result = FALSE;
                        }

                        fclose(handle);

                        if (result) {
                            if (XplStrCaseCmp(V->Buffer, Type) == 0) {
                                MDBFILEFilenameToObjectDN(Path, V->Buffer, NULL);

                                MDBFILEAddValue(V->Buffer + offset, V);
                            } else if (Flags) {
                                unsigned char    schemapath[(XPL_MAX_PATH * 4) + 1];

                                ptr = schemapath + MDBFILEClassToFilename(V->Buffer, schemapath);

                                strcpy(ptr, "/flags");
                                handle = fopen(schemapath, "rb");
                                if (handle) {
                                    if (fgets(V->Buffer, sizeof(V->Buffer), handle)) {
                                        ChopNL(V->Buffer);

                                        if (atol(V->Buffer) & 0x01) {
                                            MDBFILEFilenameToObjectDN(Path, V->Buffer, NULL);

                                            MDBFILEAddValue(V->Buffer + offset, V);
                                        }
                                    }

                                    fclose(handle);
                                }
                            }
                        }
                    } else {
                        LogMessage("[%04d] MDBFILERecurseObjects(...) open \"%s\" failed; error %d.\r\n", __LINE__, Path, errno);

                        result = FALSE;
                    }
                }
            }
        }

        XplCloseDir(parent);                            
    } else {
        result = FALSE;
    }

    return(result);
}

long MDBFILEEnumerateObjects(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, MDBValueStruct *V)
{
    unsigned long count;
    struct stat buf;

    if (V && MDB_CONTEXT_IS_VALID(V)) {
        if (Container && Container[0] && ((Container[0] != '.') || (Container[1] != '\0'))) {
            MDBFILEObjectToFilename(Container, NULL, V->Filename, V, NULL);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, NULL, V->Filename, V, NULL);
        }

        LogMessage("[%04d] MDBEnumerateObjects(\"%s\", ...) enumerating \"%s\".\r\n", __LINE__, Container, V->Filename);

        if (stat(V->Filename, &buf) == 0) {
            count = V->Used;

            MDBFILERecurseObjects(V->Filename, Type, Pattern, 0, V);

            return(V->Used - count);
        }
    }

    return(0);
}

const unsigned char *
MDBFILEEnumerateObjectsEx(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, unsigned long Flags, MDBEnumStruct *E, MDBValueStruct *V)
{
    if (E) {
        if (!E->Initialized) {
            E->V = NULL;
            E->EntriesLeft = 0;

            if (V && MDB_CONTEXT_IS_VALID(V)) {
                E->V = MDBFILEShareContext(V);
                if (E->V) {
                    if (Container && Container[0] && ((Container[0] != '.') || (Container[1] != '\0'))) {
                        MDBFILEObjectToFilename(Container, NULL, E->Filename, E->V, NULL);
                    } else {
                        MDBFILEObjectToFilename(E->V->BaseDN, NULL, E->Filename, E->V, NULL);
                    }

                    LogMessage("[%04d] MDBEnumerateObjectsEx(\"%s\", \"%s\", \"%s\", %d, ...) enumerating \"%s\".\r\n", __LINE__, Container, Type, Pattern, Flags, E->Filename);

                    if (MDBFILERecurseObjects(E->Filename, Type, Pattern, Flags, E->V)) {
                        E->Initialized = TRUE;
                        E->EntriesLeft = E->V->Used;
                    }
                }
            }
        }

        if (E->EntriesLeft) {
            E->EntriesLeft--;

            strcpy(E->Buffer, E->V->Value[0]);

            LogMessage("[%04d] MDBEnumerateObjectsEx(\"%s\", ...) returning \"%s\".\r\n", __LINE__, Container, E->Buffer);

            MDBFILEFreeValue(0, E->V);

            return(E->Buffer);
        }

        E->EntriesLeft = 0;

        if (E->V != NULL) {
            MDBFILEDestroyValueStruct(E->V);
            E->V = NULL;
        }

        E->Initialized = FALSE;
    }

    return(NULL);
}

const unsigned char *
MDBFILEEnumerateAttributesEx(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V)
{
    // LogMessage("[%04d] MDBEnumerateAttributesEx(\"%s\", ...) is not implemented\r\n", __LINE__, Object);
    if (E) {
        if (!E->Initialized) {
            E->V = NULL;
            E->EntriesLeft = 0;
                                                                                                                                       
            if (V && MDB_CONTEXT_IS_VALID(V)) {
                E->V = MDBFILEShareContext(V);
                if (E->V) {
                    if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
                        MDBFILEObjectToFilename(Object, NULL, E->Filename, E->V, NULL);
                    } else {
                        MDBFILEObjectToFilename(E->V->BaseDN, NULL, E->Filename, E->V, NULL);
                    }
                                                                                                                                       
                    LogMessage("[%04d] MDBEnumerateAttributesEx(\"%s\", ...) enumerating \"%s\".\r\n", __LINE__, Object, E->Filename);
                                                                                                                                       
                    if (MDBFILERecurseAttributes(E->Filename, E->V)) {
                        E->Initialized = TRUE;
                        E->EntriesLeft = E->V->Used;
                    }
                }
            }
        }
                                                                                                                                       
        if (E->EntriesLeft) {
            E->EntriesLeft--;
                                                                                                                                                    strcpy(E->Buffer, E->V->Value[0]);
                                                                                                                                                    LogMessage("[%04d] MDBEnumerateAttributesEx(\"%s\", ...) returning \"%s\".\r\n", __LINE__, Object, E->Buffer);
                                                                                                                                                    MDBFILEFreeValue(0, E->V);
                                                                                                                                       
            return(E->Buffer);
        }
                                                                                                                                       
        E->EntriesLeft = 0;
                                                                                                                                       
        if (E->V != NULL) {
            MDBFILEDestroyValueStruct(E->V);
            E->V = NULL;
        }
                                                                                                                                                E->Initialized = FALSE;
    }
                                                                                                                                       
    return(NULL);
}

BOOL 
MDBFILECreateAlias(const unsigned char *Alias, const unsigned char *AliasedObjectDn, MDBValueStruct *V)
{
    const unsigned char *Class = "alias";
    unsigned char *ptr;
    const unsigned char *rdn;
    FILE *handle;
    struct stat buf;
    BOOL result = FALSE;
    MDBFILESchemaClass *c = NULL;

    if (Alias && V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle) && !V->Handle->ReadOnly) {

        V->Filename[0] = '\0';

        c = MDBFILELoadSchemaClass(Class);
        if (c) {
            if (c->naming && c->naming->Used) {
                MDBFILEObjectToFilename(Alias, NULL, V->Filename, V, NULL);
                if (V->Filename[0] && (stat(V->Filename, &buf) != 0)) {
                    MDBFILECreatePath(V->Filename);

                    LogMessage("[%04d] MDBCreateObject(\"%s\", \"%s\", ...)\r\n", __LINE__, Alias, Class);

                    ptr = V->Filename + strlen(V->Filename);

                    strcpy(ptr, "/object%032class");
                    handle = fopen(V->Filename, "wb");
                    if (handle) {
                        fprintf(handle, "%s\r\n", c->name);
                        fclose(handle);
                    }

                    rdn = strrchr(Alias, '\\');
                    if (rdn) {
                        rdn++;
                    } else {
                        rdn = Alias;
                    }


                    MDBFILEObjectToFilename(NULL, c->naming->Value[0], ptr, NULL, NULL);
                    handle = fopen(V->Filename, "wb");
                    if (handle) {
                        fprintf(handle, "%s\r\n", rdn);

                        fclose(handle);
                    }

                    ptr[0] = '/';
                    MDBFILEObjectToFilename(NULL, "Aliased Object Name", ptr, NULL, NULL);
                    handle = fopen(V->Filename, "ab");
                    if (handle) {
                        if (AliasedObjectDn[0] != '\\') {
                            sprintf(V->Buffer, "%s\\%s", V->BaseDN, AliasedObjectDn);
                        } else {
                            strcpy(V->Buffer, AliasedObjectDn);
                        }

                        LogMessage("[%04d] MDBCreateObject(\"%s\", \"%s\", ...) adding DN \"%s\" = \"%s\"\r\n", __LINE__, Alias, Class, "Aliased Object Name", V->Buffer);

                        fprintf(handle, "%s\r\n", V->Buffer);


                        fclose(handle);
                    }

                    result = TRUE;
                } else {
                    V->ErrNo = ERR_ENTRY_ALREADY_EXISTS;
                }


            }
        }

    }

    if (c) {
        MDBFILEFreeSchemaClass(c);
        c = NULL;
    }
    
    return(result);
}

BOOL 
MDBFILECreateObject(const unsigned char *Object, const unsigned char *Class, MDBValueStruct *Attribute, MDBValueStruct *Data, MDBValueStruct *V)
{
    unsigned long i;
    unsigned char *ptr;
    const unsigned char *rdn;
    FILE *handle;
    struct stat buf;
    BOOL result = FALSE;
    MDBFILESchemaClass *c = NULL;

    if (Object && Class && V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle) && !V->Handle->ReadOnly) {

        V->Filename[0] = '\0';

        c = MDBFILELoadSchemaClass(Class);
        if (c) {
            if (c->naming && c->naming->Used) {
                MDBFILEObjectToFilename(Object, NULL, V->Filename, V, NULL);
            }
        }

        if (V->Filename[0] && (stat(V->Filename, &buf) != 0)) {
            MDBFILECreatePath(V->Filename);

            LogMessage("[%04d] MDBCreateObject(\"%s\", \"%s\", ...)\r\n", __LINE__, Object, Class);

            ptr = V->Filename + strlen(V->Filename);

            strcpy(ptr, "/object%032class");
            handle = fopen(V->Filename, "wb");
            if (handle) {
                fprintf(handle, "%s\r\n", Class);

                fclose(handle);
            }

            rdn = strrchr(Object, '\\');
            if (rdn) {
                rdn++;
            } else {
                rdn = Object;
            }


            MDBFILEObjectToFilename(NULL, c->naming->Value[0], ptr, NULL, NULL);
            handle = fopen(V->Filename, "wb");
            if (handle) {
                fprintf(handle, "%s\r\n", rdn);

                fclose(handle);
            }

            if (Attribute && Attribute->Used > 0 && Attribute->Used == Data->Used) {
                for (i = 0; i < Attribute->Used; i++) {
                    ptr[0] = '/';
                    MDBFILEObjectToFilename(NULL, Attribute->Value[i] + 1, ptr, NULL, NULL);
                    handle = fopen(V->Filename, "ab");
                    if (handle) {
                        if (Attribute->Value[i][0] != MDB_ATTR_SYN_DIST_NAME) {
                            unsigned char *special;
                            unsigned char *data;

                            LogMessage("[%04d] MDBCreateObject(\"%s\", \"%s\", ...) adding \"%s\" = \"%s\"\r\n", __LINE__, Object, Class, Attribute->Value[i] + 1, Data->Value[i]);

                            /* output the attribute value and escape CR, LF & \ as \r, \n & \\ respecitively */
                            data = Data->Value[i];
                            while ((special = strpbrk(data, "\n\r\\")) != NULL) {
                                fwrite(data, sizeof(unsigned char), special-data, handle);
                                fputc('\\', handle);
                                if (*special == '\r') {
                                    fputc('r', handle);
                                } else if (*special == '\n') {
                                    fputc('n', handle);
                                } else {
                                    fputc(*special, handle);
                                }
                                data = special + 1;
                            }
                            fprintf(handle, "%s\r\n", data);
                        } else {
                            if (Data->Value[i][0] != '\\') {
                                sprintf(V->Buffer, "%s\\%s", V->BaseDN, Data->Value[i]);
                            } else {
                                strcpy(V->Buffer, Data->Value[i]);
                            }

                            LogMessage("[%04d] MDBCreateObject(\"%s\", \"%s\", ...) adding DN \"%s\" = \"%s\"\r\n", __LINE__, Object, Class, Attribute->Value[i] + 1, V->Buffer);

                            fprintf(handle, "%s\r\n", V->Buffer);
                        }

                        fclose(handle);
                    }
                }
            }

            result = TRUE;
        } else {
            V->ErrNo = ERR_ENTRY_ALREADY_EXISTS;
        }
    }

    if (c) {
        MDBFILEFreeSchemaClass(c);
        c = NULL;
    }
    
    return(result);
}

static int 
DeleteRecursive(const unsigned char *DirBase, const unsigned char *DirEntry, MDBValueStruct *V)
{
    XplDir *parent;
    XplDir *entry;
    unsigned char *dirBase;

    dirBase = strdup(DirBase);
    if (dirBase) {
        sprintf(V->Filename, "%s/%s", dirBase, DirEntry);
        parent = XplOpenDir(V->Filename);    
        if (parent) {
            while ((entry = XplReadDir(parent)) != NULL) {
                if (entry->d_attr & XPL_A_SUBDIR) {
                    if ((strcmp(entry->d_name, ".") != 0) && (strcmp(entry->d_name, "..") != 0)) {
                        sprintf(V->Filename, "%s/%s", dirBase, DirEntry);
                        if (!DeleteRecursive(V->Filename, entry->d_name, V)) {
                            return(0);
                        }

                        sprintf(V->Filename, "%s/%s/%s", dirBase, DirEntry, entry->d_name);
                        rmdir(V->Filename);
                    }

                    continue;
                }

                sprintf(V->Filename, "%s/%s/%s", dirBase, DirEntry, entry->d_name);
                unlink(V->Filename);
            }

            XplCloseDir(parent);
        }

        sprintf(V->Filename, "%s/%s", dirBase, DirEntry);

        rmdir(V->Filename);

        unlink(V->Filename);

        free(dirBase);
        return(1);
    }
    return(0);
}

BOOL 
MDBFILEDeleteObject(const unsigned char *Object, BOOL Recursive, MDBValueStruct *V)
{
    BOOL result = FALSE;
    unsigned char *delim;
    XplDir *parent;
    XplDir *entry;
    struct stat buf;

    if (V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle) && !V->Handle->ReadOnly) {
        if (Object && Object[0] && ((Object[0] != '.') || (Object[1] != '\0'))) {
            MDBFILEObjectToFilename(Object, NULL, V->Filename, V, NULL);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, NULL, V->Filename, V, NULL);
        }

        delim = V->Filename + strlen(V->Filename);

        if (stat(V->Filename, &buf) == 0) {
            LogMessage("[%04d] MDBDeleteObject(\"%s\", \"%s\", ...)\r\n", __LINE__, Object, (Recursive == FALSE)? "FALSE": "TRUE");

            parent = XplOpenDir(V->Filename);
            if (parent) {
                while ((entry = XplReadDir(parent)) != NULL) {
                    if (entry->d_attr & XPL_A_SUBDIR) {
                        if ((strcmp(entry->d_name, ".") != 0) && (strcmp(entry->d_name, "..") != 0)) {
                            if (Recursive) {
                                if (DeleteRecursive(V->Filename, entry->d_name, V)) {
                                    continue;
                                }
    
                                V->ErrNo = ERR_SYSTEM_FAILURE;
                                XplCloseDir(parent);
                                return(FALSE);
                            }

                            V->ErrNo = ERR_ENTRY_IS_NOT_LEAF;

                            XplCloseDir(parent);

                            return(FALSE);
                        }

                        continue;
                    }

                    *delim = '/';
                    strcpy(delim + 1, entry->d_name);

                    unlink(V->Filename);

                    *delim = '\0';
                }

                XplCloseDir(parent);
            }

            if (rmdir(V->Filename) == 0) {
                result = TRUE;
            } else if (errno == ENOTEMPTY) {
                V->ErrNo = ERR_ENTRY_IS_NOT_LEAF;
            } else if (errno == ENOENT) {
                V->ErrNo = ERR_NO_SUCH_ENTRY;
            } else {
                V->ErrNo = ERR_TRANSPORT_FAILURE;
            }
        } else {
            V->ErrNo = ERR_NO_SUCH_ENTRY;
        }
    }

    return(result);
}

BOOL 
MDBFILERenameObject(const unsigned char *ObjectOld, const unsigned char *ObjectNew, MDBValueStruct *V)
{
    unsigned char oldShort[XPL_MAX_PATH + 1];
    unsigned char newShort[XPL_MAX_PATH + 1];
    unsigned char newName[(XPL_MAX_PATH * 4) + 1];
    unsigned char class[XPL_MAX_PATH + 1];
    struct stat buf;

    if (ObjectNew && V && MDB_CONTEXT_IS_VALID(V) && MDBFileIsValidHandle(V->Handle) && !V->Handle->ReadOnly) {
        if (ObjectOld && ObjectOld[0] && ((ObjectOld[0] != '.') || (ObjectOld[1] != '\0'))) {
            MDBFILEObjectToFilename(ObjectOld, NULL, V->Filename, V, NULL);
        } else {
            MDBFILEObjectToFilename(V->BaseDN, NULL, V->Filename, V, NULL);
        }

        if (stat(V->Filename, &buf) == 0) {
            MDBFILEObjectToFilename(ObjectNew, NULL, newName, V, NULL);
            if (stat(newName, &buf) != 0) {
                XPLLongToDos(V->Filename, oldShort, XPL_MAX_PATH);
                XPLLongToDos(newName, newShort, XPL_MAX_PATH);

                if( MDBFILEGetObjectDetails(ObjectOld, class, NULL, NULL, V) ) {
                    if (rename(oldShort, newShort) == 0) {
                        unsigned char       *ptr;
                        const unsigned char *rdn;
                        MDBFILESchemaClass  *c = NULL;

                        ptr = newShort + strlen(newShort);

                        rdn = strrchr(ObjectNew, '\\');
                        if (rdn) {
                            rdn++;
                        } else {
                            rdn = ObjectNew;
                        }

                        c = MDBFILELoadSchemaClass(class);
                        if (c) {
                            FILE *handle;
                            MDBFILEObjectToFilename(NULL, c->naming->Value[0], ptr, NULL, NULL);
                            handle = fopen(newShort, "wb");
                            if (handle) {
                                fprintf(handle, "%s\r\n", rdn);

                                fclose(handle);
                            }

                            MDBFILEFreeSchemaClass(c);
                            c = NULL;

                            return(TRUE);
                        }

                        V->ErrNo = ERR_NO_SUCH_CLASS;
                    } else {
                        V->ErrNo = ERR_ENTRY_IS_NOT_LEAF;
                    }
                } else {
                    V->ErrNo = ERR_NO_SUCH_CLASS;
                }
            } else {
                V->ErrNo = ERR_ENTRY_ALREADY_EXISTS;
            }
        } else {
            V->ErrNo = ERR_NO_SUCH_ENTRY;
        }
    }

    return(FALSE);
}

BOOL 
MDBFILEGrantObjectRights(const unsigned char *Object, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Delete, BOOL Rename, BOOL Admin, MDBValueStruct *V)
{
    LogMessage("[%04d] MDBGrantObjectRights(\"%s\", \"%s\", ...) is not implemented\r\n", __LINE__, Object, TrusteeDN);

    return(TRUE);
}

BOOL 
MDBFILEGrantAttributeRights(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Admin, MDBValueStruct *V)
{
    LogMessage("[%04d] MDBGrantAttributeRights(\"%s\", \"%s\", \"%s\", ...) is not implemented\r\n", __LINE__, Object, Attribute, TrusteeDN);

    return(TRUE);
}

BOOL 
MDBFILEAddAddressRestriction(const unsigned char *Object, const unsigned char *Server, MDBValueStruct *V)
{
    LogMessage("[%04d] MDBAddAddressRestriction(\"%s\", \"%s\", ...) is not implemented\r\n", __LINE__, Object, Server);

    return(FALSE);
}

static BOOL 
MDBFILEParseArguments(const unsigned char *Arguments)
{
    unsigned char filename[XPL_MAX_PATH + 1];
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

        if (XplStrNCaseCmp(ptr, "debug=", 6) == 0) {
            if (atoi(ptr + 6)) {
                MDBFile.log.buffer = (unsigned char *)malloc(VALUE_BUFFER_SIZE);
                if (MDBFile.log.buffer) {
                    MDBFILECreatePath(MDBFile.root);

                    sprintf(filename, "%s/log", MDBFile.root);
                    MDBFile.log.fh = fopen(filename, "wb");
                    if (MDBFile.log.fh) {
                        MDBFile.log.enabled = TRUE;
                        MDBFile.log.bufferSize = VALUE_BUFFER_SIZE;

                        if (atoi(ptr + 6) > 1) {
                            MDBFile.log.console = TRUE;
                        }

                        XplOpenLocalSemaphore(MDBFile.log.sem, 1);
                    } else {
                        free(MDBFile.log.buffer);
                    }
                }
            }
        }

        /* Restore the buffer */
        if (end) {
            *end = ',';
            ptr = end + 1;
        } else {
            ptr = NULL;
        }
    }

    if (toFree) {
        free(toFree);
    }

    return(result);
}

EXPORT BOOL 
MDBFILEInit(MDBDriverInitStruct *Init)
{
    unsigned char path[XPL_MAX_PATH + 1];
    struct stat buf;
    BOOL result = FALSE;

    MDBFile.unload = FALSE;

    strcpy(MDBFile.localTree, "\\Tree");
    strcpy(MDBFile.replicaDN, "Bongo");
    strcpy(MDBFile.base64Chars, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=");

#if defined(LINUX)
    strcpy(MDBFile.root, XPL_DEFAULT_STATE_DIR "/mdb");
    strcpy(MDBFile.schema, XPL_DEFAULT_STATE_DIR "/mdb/schema");
#else
    strcpy(MDBFile.root, "C:\\program files\\Bongo\\MDB");
    strcpy(MDBFile.schema, "C:\\program files\\Bongo\\MDB\\schema");
#endif

    MDBFile.rootLength = strlen(MDBFile.root);

    MDBFile.log.enabled = FALSE;

    XplOpenLocalSemaphore(MDBFile.handles.sem, 1);
    MDBFile.handles.data = NULL;
    MDBFile.handles.used = 0;
    MDBFile.handles.allocated = 0;

    if (Init->Arguments) {
        MDBFILEParseArguments(Init->Arguments);
    }

        strcpy(path, MDBFile.schema);

    MDBFile.directoryHandle = MDBFileCreateContext("/", FALSE);
    if (MDBFile.directoryHandle) {
        strcpy(MDBFile.directoryHandle->Description, PRODUCT_NAME);
    }

        if (stat(path, &buf) != 0) {
            unsigned char *cur;
            unsigned char *ptr;
            MDBValueStruct *v;
            MDBValueStruct *superclass;
            MDBValueStruct *containedby;
            MDBValueStruct *naming;
            MDBValueStruct *mandatory;
            MDBValueStruct *optional;
            MDBValueStruct *attributes;
            MDBValueStruct *values;
            MDBFILEBaseSchemaAttributes *a;
            MDBFILEBaseSchemaClasses *cSource;
            MDBFILEBaseSchemaClasses *c;

            v = MDBFILECreateValueStruct(MDBFile.directoryHandle, NULL);
            if (v) {
                XplMakeDir(path);

                result = TRUE;
            } else {
                result = FALSE;
            }

            a = &MDBFILEBaseSchemaAttributesList[0];
            while (result && (a->name != NULL)) {
                result = MDBFILEDefineAttribute(a->name, a->asn1, a->type, a->flags & 0x0001, a->flags & 0x0040, a->flags & 0x0080, v);

                a++;
            }

            if (result) {
                superclass = MDBFILEShareContext(v);
                containedby = MDBFILEShareContext(v);
                naming = MDBFILEShareContext(v);
                mandatory = MDBFILEShareContext(v);
                optional = MDBFILEShareContext(v);

                c = (MDBFILEBaseSchemaClasses *)malloc(sizeof(MDBFILEBaseSchemaClasses));
                if (superclass && containedby && naming && mandatory && optional && c) {
                    cSource = &MDBFILEBaseSchemaClassesList[0];
                    while (result && (cSource->name != NULL)) {
                        c->flags = cSource->flags;

                        c->name = strdup(cSource->name);

                        if (cSource->asn1) {
                            c->asn1 = strdup(cSource->asn1);
                        } else {
                            c->asn1 = NULL;
                        }

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
                            MDBFILEAddValue(c->superClass, superclass);
                        }

                        if (c->containedBy) {
                            ptr = c->containedBy;

                            do {
                                cur = ptr;
                                ptr = strchr(cur, ',');
                                if (ptr) {
                                    *ptr = '\0';
                                }

                                MDBFILEAddValue(cur, containedby);

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

                                MDBFILEAddValue(cur, naming);

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

                                MDBFILEAddValue(cur, mandatory);

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

                                MDBFILEAddValue(cur, optional);

                                if (ptr) {
                                    *ptr = ',';

                                    ptr++;
                                }
                            } while (ptr);
                        }

                        result = MDBFILEDefineClass(c->name, c->asn1, c->flags & 0x01, superclass, containedby, naming, mandatory, optional, v);

                        free(c->name);
                        c->name = NULL;

                        if (c->asn1) {
                            free(c->asn1);
                            c->asn1 = NULL;
                        }

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

                        MDBFILEFreeValues(optional);
                        MDBFILEFreeValues(mandatory);
                        MDBFILEFreeValues(naming);
                        MDBFILEFreeValues(containedby);
                        MDBFILEFreeValues(superclass);

                        cSource++;
                    }
                }

                if (optional) {
                    MDBFILEDestroyValueStruct(optional);
                }

                if (mandatory) {
                    MDBFILEDestroyValueStruct(mandatory);
                }

                if (naming) {
                    MDBFILEDestroyValueStruct(naming);
                }

                if (containedby) {
                    MDBFILEDestroyValueStruct(containedby);
                }

                if (superclass) {
                    MDBFILEDestroyValueStruct(superclass);
                }

                if (c) {
                    free(c);
                }
            }

            if (result) {
                attributes = MDBFILEShareContext(v);
                values = MDBFILEShareContext(v);

                if (attributes && values) {
                    result = MDBFILECreateObject(MDBFile.localTree, "Top", attributes, values, v);

                    if (result) {
                        MDBFILESetValueStructContext(MDBFile.localTree, v);

                        MDBFILEAddValue("SSurname", attributes);
                        MDBFILEAddValue("Admin", values);

                        result = MDBFILECreateObject("admin", "User", attributes, values, v);

                        MDBFILEFreeValues(attributes);
                        MDBFILEFreeValues(values);

                        if (result) {
                            result = MDBFILEChangePasswordEx("admin", NULL, "bongo", v);
                        }

                        MDBFILESetValueStructContext(NULL, v);
                    }
                }

                if (attributes) {
                    MDBFILEDestroyValueStruct(attributes);
                }

                if (values) {
                    MDBFILEDestroyValueStruct(values);
                }
            }

            MDBFILEDestroyValueStruct(v);
        } else {
            result = TRUE;
        }

        if (result) {
            Init->Interface.MDBAdd=MDBFILEAdd;
            Init->Interface.MDBAddAddressRestriction=MDBFILEAddAddressRestriction;
            Init->Interface.MDBAddAttribute=MDBFILEAddAttribute;
            Init->Interface.MDBAddDN=MDBFILEAddDN;
            Init->Interface.MDBAddValue=MDBFILEAddValue;
            Init->Interface.MDBAuthenticate=MDBFILEAuthenticateFunction;
            Init->Interface.MDBChangePassword=MDBFILEChangePassword;
            Init->Interface.MDBChangePasswordEx=MDBFILEChangePasswordEx;
            Init->Interface.MDBClear=MDBFILEClear;
            Init->Interface.MDBCreateAlias=MDBFILECreateAlias;
            Init->Interface.MDBCreateEnumStruct=MDBFILECreateEnumStruct;
            Init->Interface.MDBCreateObject=MDBFILECreateObject;
            Init->Interface.MDBCreateValueStruct=MDBFILECreateValueStruct;
            Init->Interface.MDBDefineAttribute=MDBFILEDefineAttribute;
            Init->Interface.MDBDefineClass=MDBFILEDefineClass;
            Init->Interface.MDBDeleteObject=MDBFILEDeleteObject;
            Init->Interface.MDBDestroyEnumStruct=MDBFILEDestroyEnumStruct;
            Init->Interface.MDBDestroyValueStruct=MDBFILEDestroyValueStruct;
            Init->Interface.MDBEnumerateAttributesEx=MDBFILEEnumerateAttributesEx;
            Init->Interface.MDBEnumerateObjects=MDBFILEEnumerateObjects;
            Init->Interface.MDBEnumerateObjectsEx=MDBFILEEnumerateObjectsEx;
            Init->Interface.MDBFreeValue=MDBFILEFreeValue;
            Init->Interface.MDBFreeValues=MDBFILEFreeValues;
            Init->Interface.MDBGetObjectDetails=MDBFILEGetObjectDetails;
            Init->Interface.MDBGetObjectDetailsEx=MDBFILEGetObjectDetailsEx;
            Init->Interface.MDBGetServerInfo=MDBFILEGetServerInfo;
            Init->Interface.MDBGetBaseDN=MDBFILEGetBaseDN;
            Init->Interface.MDBGrantAttributeRights=MDBFILEGrantAttributeRights;
            Init->Interface.MDBGrantObjectRights=MDBFILEGrantObjectRights;
            Init->Interface.MDBIsObject=MDBFILEIsObject;
            Init->Interface.MDBListContainableClasses=MDBFILEListContainableClasses;
            Init->Interface.MDBListContainableClassesEx=MDBFILEListContainableClassesEx;
            Init->Interface.MDBRead=MDBFILERead;
            Init->Interface.MDBReadDN=MDBFILEReadDN;
            Init->Interface.MDBReadEx=MDBFILEReadEx;
            Init->Interface.MDBRelease=MDBFILERelease;
            Init->Interface.MDBRemove=MDBFILERemove;
            Init->Interface.MDBRemoveDN=MDBFILERemoveDN;
            Init->Interface.MDBRenameObject=MDBFILERenameObject;
            Init->Interface.MDBSetValueStructContext=MDBFILESetValueStructContext;
            Init->Interface.MDBShareContext=MDBFILEShareContext;
            Init->Interface.MDBUndefineAttribute=MDBFILEUndefineAttribute;
            Init->Interface.MDBUndefineClass=MDBFILEUndefineClass;
            Init->Interface.MDBVerifyPassword=MDBFILEVerifyPassword;
            Init->Interface.MDBVerifyPasswordEx=MDBFILEVerifyPassword;
            Init->Interface.MDBWrite=MDBFILEWrite;
            Init->Interface.MDBWriteDN=MDBFILEWriteDN;
            Init->Interface.MDBWriteTyped=MDBFILEWriteTyped;
        }

    return(result);
}

EXPORT void 
MDBFILEShutdown(void)
{
    MDBFile.unload = TRUE;

    if (MDBFile.log.enabled) {
        XplWaitOnLocalSemaphore(MDBFile.log.sem);

        MDBFile.log.enabled = FALSE;

        fclose(MDBFile.log.fh);

        free(MDBFile.log.buffer);
        MDBFile.log.buffer = NULL;
        MDBFile.log.bufferSize = 0;

        XplCloseLocalSemaphore(MDBFile.log.sem);
    }

    if (MDBFile.handles.data) {
        unsigned long i;

        XplWaitOnLocalSemaphore(MDBFile.handles.sem);

        for (i = 0; i < MDBFile.handles.used; i++) {
            free(MDBFile.handles.data[i]);
        }

        free(MDBFile.handles.data);

        XplCloseLocalSemaphore(MDBFile.handles.sem);
    }

    return;
}

#if defined(NETWARE) || defined(LIBC)
int 
MDBFILERequestUnload(void)
{
    if (!MDBFile.unload) {
        int    OldTGid;

        OldTGid=XplSetThreadGroupID(MDBFile.id.group);

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
MDBFILESigHandler(int Signal)
{
    int    OldTGid;

    OldTGid=XplSetThreadGroupID(MDBFile.id.group);

    XplSignalLocalSemaphore(MDBFile.sem.shutdown);
    XplWaitOnLocalSemaphore(MDBFile.sem.shutdown);

    /* Do any required cleanup */
    MDBFILEShutdown();

    XplCloseLocalSemaphore(MDBFile.sem.shutdown);
    XplSetThreadGroupID(OldTGid);
}

int 
main(int argc, char *argv[])
{
    MDBFile.id.main=XplGetThreadID();
    MDBFile.id.group=XplGetThreadGroupID();

    signal(SIGTERM, MDBFILESigHandler);

    /*
        This will "park" the module 'til we get unloaded; 
        it would not be neccessary to do this on NetWare, 
        but to prevent from automatically exiting on Unix
        we need to keep main around...
    */

    XplOpenLocalSemaphore(MDBFile.sem.shutdown, 0);
    XplWaitOnLocalSemaphore(MDBFile.sem.shutdown);
    XplSignalLocalSemaphore(MDBFile.sem.shutdown);
    return(0);
}
#endif

#ifdef WIN32
BOOL WINAPI
DllMain(HINSTANCE hInst, DWORD Reason, LPVOID Reserved)
{
    if (Reason==DLL_PROCESS_ATTACH) {
        DisableThreadLibraryCalls(hInst);
    }

    return(TRUE);
}
#endif
