/****************************************************************************
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
 ****************************************************************************/


#ifndef MDB_H
#define MDB_H

/* Generic */
#define MDB_CURRENT_API_VERSION                 1
#define MDB_COMPATIBLE_TO_API                   1
#define MDB_MAX_API_DESCRIPTION_CHARS           256

/* Length definitions */
#define MDB_MAX_OBJECT_CHARS                    256
#define MDB_MAX_UTF8_OBJECT_CHARS               768
#define MDB_MAX_ATTRIBUTE_VALUE_CHARS           10240
#define MDB_MAX_ATTRIBUTE_VALUE_UTF8_CHARS      (10240*3)
#define MDB_MAX_TREE_CHARS                      32
#define MDB_MAX_ATTRIBUTE_CHARS                 32

/* Attribute types */
#define MDB_ATTR_SYN_STRING                     'S'
#define MDB_ATTR_SYN_DIST_NAME                  'D'
#define MDB_ATTR_SYN_BOOL                       'B'
#define MDB_ATTR_SYN_BINARY                     'O'

/* Little helpers */
#define MDB_IS_MDB_API_COMPATIBLE(Handle)       (MDBGetAPIVersion(TRUE, NULL, Handle)<=MDB_CURRENT_API_VERSION)
#define MDB_CURRENT_CONTEXT                     "."
#define MDBGetLastError(ValueStruct)            (ValueStruct->ErrNo)

/* Depth */
#define MDB_SCOPE_BASE 0
#define MDB_SCOPE_ONELEVEL 1
#define MDB_SCOPE_INFINITE 2

/* Used for MDBCreateObject */
#ifndef __cplusplus
#define MDBAddStringAttribute(AttrName, AttrValue, AttrStruct, DataStruct)      { unsigned char Temp[MDB_MAX_UTF8_OBJECT_CHARS]; Temp[0]=MDB_ATTR_SYN_STRING; strcpy(Temp+1, (unsigned char *)((AttrName))); MDBAddValue(Temp, (AttrStruct)); MDBAddValue((const unsigned char *)((AttrValue)), (DataStruct)); }
#define MDBAddDNAttribute(AttrName, AttrValue, AttrStruct, DataStruct)          { unsigned char Temp[MDB_MAX_UTF8_OBJECT_CHARS]; Temp[0]=MDB_ATTR_SYN_DIST_NAME; strcpy(Temp+1, (unsigned char *)((AttrName))); MDBAddValue(Temp, (AttrStruct)); MDBAddValue((const unsigned char *)((AttrValue)), (DataStruct)); }
#endif

#if !defined(MDB_INTERNAL)
typedef void *                 MDBHandle;
typedef void                   MDBEnumStruct;

typedef struct {
    unsigned char              **Value;
    unsigned long              Used;
    unsigned long              ErrNo;
    struct _MDBInterfaceStruct *Interface;
} MDBValueStruct;
#endif

typedef struct _MDBInterfaceStruct {
    BOOL                        (* MDBGetServerInfo)(unsigned char *HostDN, unsigned char *HostTree, MDBValueStruct *V);
    MDBHandle                   (* MDBAuthenticate)(const unsigned char *Object, const unsigned char *Password, const unsigned char *Arguments);
    BOOL                        (* MDBRelease)(MDBHandle Handle);
    MDBValueStruct              *(* MDBCreateValueStruct)(MDBHandle Handle, const unsigned char *Context);
    BOOL                        (* MDBDestroyValueStruct)(MDBValueStruct *V);
    MDBValueStruct              *(* MDBShareContext)(MDBValueStruct *VOld);
    BOOL                        (* MDBSetValueStructContext)(const unsigned char *Context, MDBValueStruct *V);
    MDBEnumStruct               *(* MDBCreateEnumStruct)(MDBValueStruct *V);
    BOOL                        (* MDBDestroyEnumStruct)(MDBEnumStruct *E, MDBValueStruct *V);
    BOOL                        (* MDBAddValue)(const unsigned char *Value, MDBValueStruct *V);
    BOOL                        (* MDBFreeValue)(unsigned long Index, MDBValueStruct *V);
    BOOL                        (* MDBFreeValues)(MDBValueStruct *V);
    long                        (* MDBRead)(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
    const unsigned char         *(* MDBReadEx)(const unsigned char *Object, const unsigned char *Attribute, MDBEnumStruct *E, MDBValueStruct *V);
    long                        (* MDBReadDN)(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
    BOOL                        (* MDBWriteTyped)(const unsigned char *Object, const unsigned char *Attribute, const int AttrType, MDBValueStruct *V);
    BOOL                        (* MDBWrite)(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
    BOOL                        (* MDBWriteDN)(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
    BOOL                        (* MDBClear)(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
    BOOL                        (* MDBAdd)(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
    BOOL                        (* MDBAddDN)(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
    BOOL                        (* MDBRemove)(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
    BOOL                        (* MDBRemoveDN)(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
    BOOL                        (* MDBIsObject)(const unsigned char *Object, MDBValueStruct *V);
    BOOL                        (* MDBGetObjectDetails)(const unsigned char *Object, unsigned char *Type, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V);
    BOOL                        (* MDBGetObjectDetailsEx)(const unsigned char *Object, MDBValueStruct *Types, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V);
    BOOL                        (* MDBVerifyPassword)(const unsigned char *Object, const unsigned char *Password, MDBValueStruct *V);
    BOOL                        (* MDBVerifyPasswordEx)(const unsigned char *Object, const unsigned char *Password, MDBValueStruct *V); /* Better error reporting */
    BOOL                        (* MDBChangePassword)(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V);
    BOOL                        (* MDBChangePasswordEx)(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V);
    long                        (* MDBEnumerateObjects)(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, MDBValueStruct *V);
    const unsigned char         *(* MDBEnumerateObjectsEx)(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, unsigned long Flags, MDBEnumStruct *E, MDBValueStruct *V);
    BOOL                        (* MDBAddAddressRestriction)(const unsigned char *Object, const unsigned char *Server, MDBValueStruct *V);
    const unsigned char         *(* MDBEnumerateAttributesEx)(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V);
    BOOL                        (* MDBCreateObject)(const unsigned char *Object, const unsigned char *Class, MDBValueStruct *Attribute, MDBValueStruct *Data, MDBValueStruct *V);
    BOOL                        (* MDBDeleteObject)(const unsigned char *Object, BOOL Recursive, MDBValueStruct *V);
    BOOL                        (* MDBRenameObject)(const unsigned char *ObjectOld, const unsigned char *ObjectNew, MDBValueStruct *V);
    BOOL                        (* MDBDefineAttribute)(const unsigned char *Attribute, const unsigned char *ASN1, unsigned long Type, BOOL SingleValue, BOOL ImmediateSync, BOOL Public, MDBValueStruct *V);
    BOOL                        (* MDBUndefineAttribute)(const unsigned char *Attribute, MDBValueStruct *V);
    BOOL                        (* MDBDefineClass)(const unsigned char *Class, const unsigned char *ASN1, BOOL Container, MDBValueStruct *Superclass, MDBValueStruct *Containment, MDBValueStruct *Naming, MDBValueStruct *Mandatory, MDBValueStruct *Optional, MDBValueStruct *V);
    BOOL                        (* MDBAddAttribute)(const unsigned char *Attribute, const unsigned char *Class, MDBValueStruct *V);
    BOOL                        (* MDBUndefineClass)(const unsigned char *Class, MDBValueStruct *V);
    BOOL                        (* MDBListContainableClasses)(const unsigned char *Container, MDBValueStruct *V);
    const unsigned char         *(* MDBListContainableClassesEx)(const unsigned char *Container, MDBEnumStruct *E, MDBValueStruct *V);
    BOOL                        (* MDBGrantObjectRights)(const unsigned char *Object, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Delete, BOOL Rename, BOOL Admin, MDBValueStruct *V);
    BOOL                        (* MDBGrantAttributeRights)(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Admin, MDBValueStruct *V);
    char                        *(* MDBGetBaseDN)(MDBValueStruct *V);
    BOOL                        (* MDBCreateAlias)(const unsigned char *Alias, const unsigned char *AliasedObjectDn, MDBValueStruct *V);
    BOOL                        (* MDBFindObjects)(const unsigned char *Container,
                                                   MDBValueStruct *Types,
                                                   const unsigned char *Pattern,
                                                   MDBValueStruct *Attributes,
                                                   int depth, 
                                                   int max,
                                                   MDBValueStruct *V);
    BOOL                        (* Reserved3)(void);
    BOOL                        (* Reserved4)(void);
    BOOL                        (* Reserved5)(void);
    BOOL                        (* Reserved6)(void);
    BOOL                        (* Reserved7)(void);
    BOOL                        (* Reserved8)(void);
    BOOL                        (* Reserved9)(void);
    BOOL                        (* Reserved10)(void);
    BOOL                        (* Reserved11)(void);
    BOOL                        (* Reserved12)(void);
    BOOL                        (* Reserved13)(void);
    BOOL                        (* Reserved14)(void);
    BOOL                        (* Reserved15)(void);
    BOOL                        (* Reserved16)(void);
    BOOL                        (* Reserved17)(void);
    BOOL                        (* Reserved18)(void);
    BOOL                        (* Reserved19)(void);
} MDBInterfaceStruct;

typedef struct {
    unsigned char           Error[128];
    MDBInterfaceStruct      Interface;
    const unsigned char     *Arguments;
} MDBDriverInitStruct;

typedef BOOL                    (* MDBDriverInitFunc)(MDBDriverInitStruct *Init);
typedef BOOL                    (* MDBDriverShutdownFunc)(void);

#ifdef __cplusplus
extern "C" {
#endif

/* Prototypes for generic things */
EXPORT BOOL                     MDBInit(void);
EXPORT BOOL                     MDBShutdown(void);

EXPORT int                      MDBGetAPIVersion(BOOL WantCompatibleVersion, unsigned char *Description, MDBHandle Context);
EXPORT BOOL                     MDBGetServerInfo(unsigned char *HostDN, unsigned char *HostTree, MDBValueStruct *V); /* Deprecated */
EXPORT char                     *MDBGetBaseDN(MDBValueStruct *V);

/* Authentication */
EXPORT MDBHandle                MDBAuthenticate(const unsigned char *Module, const unsigned char *Object, const unsigned char *Password);
EXPORT BOOL                     MDBRelease(MDBHandle Context);

/* ValueStruct initialization & manipulation */
EXPORT MDBValueStruct           *MDBCreateValueStruct(MDBHandle Handle, const unsigned char *Context);
EXPORT BOOL                     MDBDestroyValueStruct(MDBValueStruct *V);
EXPORT MDBValueStruct           *MDBShareContext(MDBValueStruct *VOld);
EXPORT BOOL                     MDBSetValueStructContext(const unsigned char *Context, MDBValueStruct *V);

EXPORT MDBEnumStruct            *MDBCreateEnumStruct(MDBValueStruct *V);
EXPORT BOOL                     MDBDestroyEnumStruct(MDBEnumStruct *E, MDBValueStruct *V);

EXPORT BOOL                     MDBAddValue(const unsigned char *Value, MDBValueStruct *V);
EXPORT BOOL                     MDBFreeValue(unsigned long Index, MDBValueStruct *V);
EXPORT BOOL                     MDBFreeValues(MDBValueStruct *V);


/* Attribute operations */
EXPORT long                     MDBRead(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
EXPORT const unsigned char      *MDBReadEx(const unsigned char *Object, const unsigned char *Attribute, MDBEnumStruct *E, MDBValueStruct *V);
EXPORT long                     MDBReadDN(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
EXPORT BOOL                     MDBWriteTyped(const unsigned char *Object, const unsigned char *Attribute, const int AttrType, MDBValueStruct *V);
EXPORT BOOL                     MDBWrite(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
EXPORT BOOL                     MDBWriteDN(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);
EXPORT BOOL                     MDBClear(const unsigned char *Object, const unsigned char *Attribute, MDBValueStruct *V);

/* Advanced attribute operations */
EXPORT BOOL                     MDBAdd(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
EXPORT BOOL                     MDBAddDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
EXPORT BOOL                     MDBRemove(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);
EXPORT BOOL                     MDBRemoveDN(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *Value, MDBValueStruct *V);

/* Object operations */
EXPORT BOOL                     MDBIsObject(const unsigned char *Object, MDBValueStruct *V);
EXPORT BOOL                     MDBGetObjectDetails(const unsigned char *Object, unsigned char *Type, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V);
EXPORT BOOL                     MDBGetObjectDetailsEx(const unsigned char *Object, MDBValueStruct *Types, unsigned char *RDN, unsigned char *DN, MDBValueStruct *V);
EXPORT BOOL                     MDBVerifyPassword(const unsigned char *Object, const unsigned char *Password, MDBValueStruct *V);
EXPORT BOOL                     MDBVerifyPasswordEx(const unsigned char *Object, const unsigned char *Password, MDBValueStruct *V); /* Better error reporting */
EXPORT BOOL                     MDBChangePassword(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V);
EXPORT BOOL                     MDBChangePasswordEx(const unsigned char *Object, const unsigned char *OldPassword, const unsigned char *NewPassword, MDBValueStruct *V);
EXPORT long                     MDBEnumerateObjects(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, MDBValueStruct *V);
EXPORT const unsigned char      *MDBEnumerateObjectsEx(const unsigned char *Container, const unsigned char *Type, const unsigned char *Pattern, unsigned long Flags, MDBEnumStruct *E, MDBValueStruct *V);

EXPORT BOOL                     MDBFindObjects(const unsigned char *Container,
                                               MDBValueStruct *Types,
                                               const unsigned char *Pattern,
                                               MDBValueStruct *Attributes,
                                               int depth, 
                                               int max,
                                               MDBValueStruct *V);
                                                 

/* Advanced Object operations */
EXPORT const unsigned char      *MDBEnumerateAttributesEx(const unsigned char *Object, MDBEnumStruct *E, MDBValueStruct *V);

/* Object manipulation */
EXPORT BOOL                     MDBCreateAlias(const unsigned char *Alias, const unsigned char *AliasedObjectDn, MDBValueStruct *V);
EXPORT BOOL                     MDBCreateObject(const unsigned char *Object, const unsigned char *Class, MDBValueStruct *Attribute, MDBValueStruct *Data, MDBValueStruct *V);
EXPORT BOOL                     MDBDeleteObject(const unsigned char *Object, BOOL Recursive, MDBValueStruct *V);
EXPORT BOOL                     MDBRenameObject(const unsigned char *ObjectOld, const unsigned char *ObjectNew, MDBValueStruct *V);

/* Schema manipulation */
EXPORT BOOL                     MDBDefineAttribute(const unsigned char *Attribute, const unsigned char *ASN1, unsigned long Type, BOOL SingleValue, BOOL ImmediateSync, BOOL Public, MDBValueStruct *V);
EXPORT BOOL                     MDBUndefineAttribute(const unsigned char *Attribute, MDBValueStruct *V);

EXPORT BOOL                     MDBDefineClass(const unsigned char *Class, const unsigned char *ASN1, BOOL Container, MDBValueStruct *Superclass, MDBValueStruct *Containment, MDBValueStruct *Naming, MDBValueStruct *Mandatory, MDBValueStruct *Optional, MDBValueStruct *V);
EXPORT BOOL                     MDBAddAttribute(const unsigned char *Attribute, const unsigned char *Class, MDBValueStruct *V);
EXPORT BOOL                     MDBUndefineClass(const unsigned char *Class, MDBValueStruct *V);

EXPORT BOOL                     MDBListContainableClasses(const unsigned char *Container, MDBValueStruct *V);
EXPORT const unsigned char      *MDBListContainableClassesEx(const unsigned char *Container, MDBEnumStruct *E, MDBValueStruct *V);

/* Object rights */
EXPORT BOOL                     MDBGrantObjectRights(const unsigned char *Object, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Delete, BOOL Rename, BOOL Admin, MDBValueStruct *V);
EXPORT BOOL                     MDBGrantAttributeRights(const unsigned char *Object, const unsigned char *Attribute, const unsigned char *TrusteeDN, BOOL Read, BOOL Write, BOOL Admin, MDBValueStruct *V);

/* Debug helper */
EXPORT BOOL                     MDBDumpValueStruct(MDBValueStruct *V);

#ifdef __cplusplus
}
#endif
#endif /* MDB_H */
