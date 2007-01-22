/****************************************************************************
 *
 * Copyright (c) 2006 Novell, Inc.
 * All Rights Reserved.
 *
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2.1 of the GNU Lesser General Public
 * License as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with this program; if not, contact Novell, Inc.
 *
 * To contact Novell about this file by physical or electronic mail,
 * you may find current contact information at www.novell.com
 *
 ****************************************************************************/

#include <Python.h>
#include <xpl.h>
#include <memmgr.h>
#include <mdb.h>

#include "libs.h"

#ifdef __cplusplus
extern "C" {
#endif

PyObject *mdb_Init(PyObject *self, PyObject *args);
PyObject *mdb_Shutdown(PyObject *self, PyObject *args);
PyObject *mdb_GetAPIVersion(PyObject *self, PyObject *args);
PyObject *mdb_GetAPIDescription(PyObject *self, PyObject *args);
PyObject *mdb_GetBaseDN(PyObject *self, PyObject *args);
PyObject *mdb_Authenticate(PyObject *self, PyObject *args);
PyObject *mdb_Release(PyObject *self, PyObject *args);
PyObject *mdb_CreateValueStruct(PyObject *self, PyObject *args);
PyObject *mdb_DestroyValueStruct(PyObject *self, PyObject *args);
PyObject *mdb_VSGetUsed(PyObject *self, PyObject *args);
PyObject *mdb_VSGetValue(PyObject *self, PyObject *args);
PyObject *mdb_VSGetErrNo(PyObject *self, PyObject *args);
PyObject *mdb_VSAddValue(PyObject *self, PyObject *args);
PyObject *mdb_VSFreeValue(PyObject *self, PyObject *args);
PyObject *mdb_VSFreeValues(PyObject *self, PyObject *args);
PyObject *mdb_VSSetContext(PyObject *self, PyObject *args);
PyObject *mdb_ShareContext(PyObject *self, PyObject *args);
PyObject *mdb_CreateEnumStruct(PyObject *self, PyObject *args);
PyObject *mdb_DestroyEnumStruct(PyObject *self, PyObject *args);
PyObject *mdb_Read(PyObject *self, PyObject *args);
PyObject *mdb_ReadEx(PyObject *self, PyObject *args);
PyObject *mdb_ReadDN(PyObject *self, PyObject *args);
PyObject *mdb_Write(PyObject *self, PyObject *args);
PyObject *mdb_WriteTyped(PyObject *self, PyObject *args);
PyObject *mdb_WriteDN(PyObject *self, PyObject *args);
PyObject *mdb_Clear(PyObject *self, PyObject *args);
PyObject *mdb_Add(PyObject *self, PyObject *args);
PyObject *mdb_AddDN(PyObject *self, PyObject *args);
PyObject *mdb_Remove(PyObject *self, PyObject *args);
PyObject *mdb_RemoveDN(PyObject *self, PyObject *args);
PyObject *mdb_IsObject(PyObject *self, PyObject *args);
PyObject *mdb_GetObjectDetails_Type(PyObject *self, PyObject *args);
PyObject *mdb_GetObjectDetails_RDN(PyObject *self, PyObject *args);
PyObject *mdb_GetObjectDetails_DN(PyObject *self, PyObject *args);
PyObject *mdb_VerifyPassword(PyObject *self, PyObject *args);
PyObject *mdb_VerifyPasswordEx(PyObject *self, PyObject *args);
PyObject *mdb_ChangePassword(PyObject *self, PyObject *args);
PyObject *mdb_ChangePasswordEx(PyObject *self, PyObject *args);
PyObject *mdb_EnumerateObjects(PyObject *self, PyObject *args);
PyObject *mdb_EnumerateObjectsEx(PyObject *self, PyObject *args);
PyObject *mdb_EnumerateAttributesEx(PyObject *self, PyObject *args);
PyObject *mdb_CreateObject(PyObject *self, PyObject *args);
PyObject *mdb_DeleteObject(PyObject *self, PyObject *args);
PyObject *mdb_RenameObject(PyObject *self, PyObject *args);
PyObject *mdb_DefineAttribute(PyObject *self, PyObject *args);
PyObject *mdb_UndefineAttribute(PyObject *self, PyObject *args);
PyObject *mdb_AddAttribute(PyObject *self, PyObject *args);
PyObject *mdb_DefineClass(PyObject *self, PyObject *args);
PyObject *mdb_UndefineClass(PyObject *self, PyObject *args);
PyObject *mdb_ListContainableClasses(PyObject *self, PyObject *args);
PyObject *mdb_ListContainableClassesEx(PyObject *self, PyObject *args);
PyObject *mdb_GrantObjectRights(PyObject *self, PyObject *args);
PyObject *mdb_GrantAttributeRights(PyObject *self, PyObject *args);
PyObject *mdb_DumpValueStruct(PyObject *self, PyObject *args);

PyMethodDef MdbMethods[] = {
    {"mdb_Init", mdb_Init, METH_VARARGS | METH_STATIC},
    {"mdb_Shutdown", mdb_Shutdown, METH_VARARGS | METH_STATIC},
    {"mdb_GetAPIVersion", mdb_GetAPIVersion, METH_VARARGS | METH_STATIC},
    {"mdb_GetAPIDescription", mdb_GetAPIDescription,
     METH_VARARGS | METH_STATIC},
    {"mdb_GetBaseDN", mdb_GetBaseDN, METH_VARARGS | METH_STATIC},
    {"mdb_Authenticate", mdb_Authenticate, METH_VARARGS | METH_STATIC},
    {"mdb_Release", mdb_Release, METH_VARARGS | METH_STATIC},
    {"mdb_CreateValueStruct", mdb_CreateValueStruct, 
     METH_VARARGS | METH_STATIC},
    {"mdb_DestroyValueStruct", mdb_DestroyValueStruct, 
     METH_VARARGS | METH_STATIC},
    {"mdb_VSGetUsed", mdb_VSGetUsed, METH_VARARGS | METH_STATIC},
    {"mdb_VSGetValue", mdb_VSGetValue, METH_VARARGS | METH_STATIC},
    {"mdb_VSGetErrNo", mdb_VSGetErrNo, METH_VARARGS | METH_STATIC},
    {"mdb_VSAddValue", mdb_VSAddValue, METH_VARARGS | METH_STATIC},
    {"mdb_VSFreeValue", mdb_VSFreeValue, METH_VARARGS | METH_STATIC},
    {"mdb_VSFreeValues", mdb_VSFreeValues, METH_VARARGS | METH_STATIC},
    {"mdb_VSSetContext", mdb_VSSetContext, METH_VARARGS | METH_STATIC},
    {"mdb_ShareContext", mdb_ShareContext, METH_VARARGS | METH_STATIC},
    {"mdb_CreateEnumStruct", mdb_CreateEnumStruct,
     METH_VARARGS | METH_STATIC},
    {"mdb_DestroyEnumStruct", mdb_DestroyEnumStruct,
     METH_VARARGS | METH_STATIC},
    {"mdb_Read", mdb_Read, METH_VARARGS | METH_STATIC},
    {"mdb_ReadEx", mdb_ReadEx, METH_VARARGS | METH_STATIC},
    {"mdb_ReadDN", mdb_ReadDN, METH_VARARGS | METH_STATIC},
    {"mdb_Write", mdb_Write, METH_VARARGS | METH_STATIC},
    {"mdb_WriteTyped", mdb_WriteTyped, METH_VARARGS | METH_STATIC},
    {"mdb_WriteDN", mdb_WriteDN, METH_VARARGS | METH_STATIC},
    {"mdb_Clear", mdb_Clear, METH_VARARGS | METH_STATIC},
    {"mdb_Add", mdb_Add, METH_VARARGS | METH_STATIC},
    {"mdb_AddDN", mdb_AddDN, METH_VARARGS | METH_STATIC},
    {"mdb_Remove", mdb_Remove, METH_VARARGS | METH_STATIC},
    {"mdb_RemoveDN", mdb_RemoveDN, METH_VARARGS | METH_STATIC},
    {"mdb_IsObject", mdb_IsObject, METH_VARARGS | METH_STATIC},
    {"mdb_GetObjectDetails_Type", mdb_GetObjectDetails_Type,
     METH_VARARGS | METH_STATIC},
    {"mdb_GetObjectDetails_RDN", mdb_GetObjectDetails_RDN,
     METH_VARARGS | METH_STATIC},
    {"mdb_GetObjectDetails_DN", mdb_GetObjectDetails_DN,
     METH_VARARGS | METH_STATIC},
    {"mdb_VerifyPassword", mdb_VerifyPassword, METH_VARARGS | METH_STATIC},
    {"mdb_VerifyPasswordEx", mdb_VerifyPasswordEx, METH_VARARGS | METH_STATIC},
    {"mdb_ChangePassword", mdb_ChangePassword, METH_VARARGS | METH_STATIC},
    {"mdb_ChangePasswordEx", mdb_ChangePasswordEx, METH_VARARGS | METH_STATIC},
    {"mdb_EnumerateObjects", mdb_EnumerateObjects, METH_VARARGS | METH_STATIC},
    {"mdb_EnumerateObjectsEx", mdb_EnumerateObjectsEx,
     METH_VARARGS | METH_STATIC},
    {"mdb_EnumerateAttributesEx", mdb_EnumerateAttributesEx,
     METH_VARARGS | METH_STATIC},
    {"mdb_CreateObject", mdb_CreateObject, METH_VARARGS | METH_STATIC},
    {"mdb_DeleteObject", mdb_DeleteObject, METH_VARARGS | METH_STATIC},
    {"mdb_RenameObject", mdb_RenameObject, METH_VARARGS | METH_STATIC},
    {"mdb_DefineAttribute", mdb_DefineAttribute, METH_VARARGS | METH_STATIC},
    {"mdb_UndefineAttribute", mdb_UndefineAttribute,
     METH_VARARGS | METH_STATIC},
    {"mdb_AddAttribute", mdb_AddAttribute, METH_VARARGS | METH_STATIC},
    {"mdb_DefineClass", mdb_DefineClass, METH_VARARGS | METH_STATIC},
    {"mdb_UndefineClass", mdb_UndefineClass, METH_VARARGS | METH_STATIC},
    {"mdb_ListContainableClasses", mdb_ListContainableClasses,
     METH_VARARGS | METH_STATIC},
    {"mdb_ListContainableClassesEx", mdb_ListContainableClassesEx,
     METH_VARARGS | METH_STATIC},
    {"mdb_GrantObjectRights", mdb_GrantObjectRights,
     METH_VARARGS | METH_STATIC},
    {"mdb_GrantAttributeRights", mdb_GrantAttributeRights,
     METH_VARARGS | METH_STATIC},
    {"mdb_DumpValueStruct", mdb_DumpValueStruct, METH_VARARGS | METH_STATIC},
    {NULL, NULL}
};

EnumItemDef MdbEnums[] = {
    {"MDB_CURRENT_API_VERSION", MDB_CURRENT_API_VERSION, NULL},
    {"MDB_COMPATIBLE_TO_API", MDB_COMPATIBLE_TO_API, NULL},
    {"MDB_MAX_OBJECT_CHARS", MDB_MAX_OBJECT_CHARS, NULL},
    {"MDB_MAX_UTF8_OBJECT_CHARS", MDB_MAX_UTF8_OBJECT_CHARS, NULL},
    {"MDB_MAX_ATTRIBUTE_VALUE_CHARS", MDB_MAX_ATTRIBUTE_VALUE_CHARS, NULL},
    {"MDB_MAX_ATTRIBUTE_VALUE_UTF8_CHARS", MDB_MAX_ATTRIBUTE_VALUE_UTF8_CHARS,
     NULL},
    {"MDB_MAX_TREE_CHARS", MDB_MAX_TREE_CHARS, NULL},
    {"MDB_MAX_ATTRIBUTE_CHARS", MDB_MAX_ATTRIBUTE_CHARS, NULL},
    /* note: the following are single characters, taken as longs... */
    {"MDB_ATTR_SYN_STRING", MDB_ATTR_SYN_STRING, 0},
    {"MDB_ATTR_SYN_DIST_NAME", MDB_ATTR_SYN_DIST_NAME, 0},
    {"MDB_ATTR_SYN_BOOL", MDB_ATTR_SYN_BOOL, 0},
    {"MDB_ATTR_SYN_BINARY", MDB_ATTR_SYN_BINARY, 0},
    {"C_AFP_SERVER", 0, C_AFP_SERVER},
    {"C_ALIAS", 0, C_ALIAS},
    {"C_BINDERY_OBJECT", 0, C_BINDERY_OBJECT},
    {"C_BINDERY_QUEUE", 0, C_BINDERY_QUEUE},
    {"C_COMMEXEC", 0, C_COMMEXEC},
    {"C_COMPUTER", 0, C_COMPUTER},
    {"C_COUNTRY", 0, C_COUNTRY},
    {"C_DEVICE", 0, C_DEVICE},
    {"C_DIRECTORY_MAP", 0, C_DIRECTORY_MAP},
    {"C_EXTERNAL_ENTITY", 0, C_EXTERNAL_ENTITY},
    {"C_GROUP", 0, C_GROUP},
    {"C_LIST", 0, C_LIST},
    {"C_LOCALITY", 0, C_LOCALITY},
    {"C_MESSAGE_ROUTING_GROUP", 0, C_MESSAGE_ROUTING_GROUP},
    {"C_MESSAGING_ROUTING_GROUP", 0, C_MESSAGING_ROUTING_GROUP},
    {"C_MESSAGING_SERVER", 0, C_MESSAGING_SERVER},
    {"C_NCP_SERVER", 0, C_NCP_SERVER},
    {"C_ORGANIZATION", 0, C_ORGANIZATION},
    {"C_ORGANIZATIONAL_PERSON", 0, C_ORGANIZATIONAL_PERSON},
    {"C_ORGANIZATIONAL_ROLE", 0, C_ORGANIZATIONAL_ROLE},
    {"C_ORGANIZATIONAL_UNIT", 0, C_ORGANIZATIONAL_UNIT},
    {"C_PARTITION", 0, C_PARTITION},
    {"C_PERSON", 0, C_PERSON},
    {"C_PRINT_SERVER", 0, C_PRINT_SERVER},
    {"C_PRINTER", 0, C_PRINTER},
    {"C_PROFILE", 0, C_PROFILE},
    {"C_QUEUE", 0, C_QUEUE},
    {"C_RESOURCE", 0, C_RESOURCE},
    {"C_SERVER", 0, C_SERVER},
    {"C_TOP", 0, C_TOP},
    {"C_TREE_ROOT", 0, C_TREE_ROOT},
    {"C_UNKNOWN", 0, C_UNKNOWN},
    {"C_USER", 0, C_USER},
    {"C_VOLUME", 0, C_VOLUME},
    {"A_ACCOUNT_BALANCE", 0, A_ACCOUNT_BALANCE},
    {"A_ACL", 0, A_ACL},
    {"A_ALIASED_OBJECT_NAME", 0, A_ALIASED_OBJECT_NAME},
    {"A_ALLOW_UNLIMITED_CREDIT", 0, A_ALLOW_UNLIMITED_CREDIT},
    {"A_AUTHORITY_REVOCATION", 0, A_AUTHORITY_REVOCATION},
    {"A_BACK_LINK", 0, A_BACK_LINK},
    {"A_BINDERY_OBJECT_RESTRICTION", 0, A_BINDERY_OBJECT_RESTRICTION},
    {"A_BINDERY_PROPERTY", 0, A_BINDERY_PROPERTY},
    {"A_BINDERY_TYPE", 0, A_BINDERY_TYPE},
    {"A_CARTRIDGE", 0, A_CARTRIDGE},
    {"A_CA_PRIVATE_KEY", 0, A_CA_PRIVATE_KEY},
    {"A_CA_PUBLIC_KEY", 0, A_CA_PUBLIC_KEY},
    {"A_CERTIFICATE_REVOCATION", 0, A_CERTIFICATE_REVOCATION},
    {"A_CERTIFICATE_VALIDITY_INTERVAL", 0, A_CERTIFICATE_VALIDITY_INTERVAL},
    {"A_COMMON_NAME", 0, A_COMMON_NAME},
    {"A_CONVERGENCE", 0, A_CONVERGENCE},
    {"A_COUNTRY_NAME", 0, A_COUNTRY_NAME},
    {"A_CROSS_CERTIFICATE_PAIR", 0, A_CROSS_CERTIFICATE_PAIR},
    {"A_DEFAULT_QUEUE", 0, A_DEFAULT_QUEUE},
    {"A_DESCRIPTION", 0, A_DESCRIPTION},
    {"A_DETECT_INTRUDER", 0, A_DETECT_INTRUDER},
    {"A_DEVICE", 0, A_DEVICE},
    {"A_DS_REVISION", 0, A_DS_REVISION},
    {"A_EMAIL_ADDRESS", 0, A_EMAIL_ADDRESS},
    {"A_EQUIVALENT_TO_ME", 0, A_EQUIVALENT_TO_ME},
    {"A_EXTERNAL_NAME", 0, A_EXTERNAL_NAME},
    {"A_EXTERNAL_SYNCHRONIZER", 0, A_EXTERNAL_SYNCHRONIZER},
    {"A_FACSIMILE_TELEPHONE_NUMBER", 0, A_FACSIMILE_TELEPHONE_NUMBER},
    {"A_FULL_NAME", 0, A_FULL_NAME},
    {"A_GENERATIONAL_QUALIFIER", 0, A_GENERATIONAL_QUALIFIER},
    {"A_GID", 0, A_GID},
    {"A_GIVEN_NAME", 0, A_GIVEN_NAME},
    {"A_GROUP_MEMBERSHIP", 0, A_GROUP_MEMBERSHIP},
    {"A_HIGH_SYNC_INTERVAL", 0, A_HIGH_SYNC_INTERVAL},
    {"A_HIGHER_PRIVILEGES", 0, A_HIGHER_PRIVILEGES},
    {"A_HOME_DIRECTORY", 0, A_HOME_DIRECTORY},
    {"A_HOST_DEVICE", 0, A_HOST_DEVICE},
    {"A_HOST_RESOURCE_NAME", 0, A_HOST_RESOURCE_NAME},
    {"A_HOST_SERVER", 0, A_HOST_SERVER},
    {"A_INHERITED_ACL", 0, A_INHERITED_ACL},
    {"A_INITIALS", 0, A_INITIALS},
    {"A_INTERNET_EMAIL_ADDRESS", 0, A_INTERNET_EMAIL_ADDRESS},
    {"A_INTRUDER_ATTEMPT_RESET_INTRVL", 0, A_INTRUDER_ATTEMPT_RESET_INTRVL},
    {"A_INTRUDER_LOCKOUT_RESET_INTRVL", 0, A_INTRUDER_LOCKOUT_RESET_INTRVL},
    {"A_LOCALITY_NAME", 0, A_LOCALITY_NAME},
    {"A_LANGUAGE", 0, A_LANGUAGE},
    {"A_LAST_LOGIN_TIME", 0, A_LAST_LOGIN_TIME},
    {"A_LAST_REFERENCED_TIME", 0, A_LAST_REFERENCED_TIME},
    {"A_LOCKED_BY_INTRUDER", 0, A_LOCKED_BY_INTRUDER},
    {"A_LOCKOUT_AFTER_DETECTION", 0, A_LOCKOUT_AFTER_DETECTION},
    {"A_LOGIN_ALLOWED_TIME_MAP", 0, A_LOGIN_ALLOWED_TIME_MAP},
    {"A_LOGIN_DISABLED", 0, A_LOGIN_DISABLED},
    {"A_LOGIN_EXPIRATION_TIME", 0, A_LOGIN_EXPIRATION_TIME},
    {"A_LOGIN_GRACE_LIMIT", 0, A_LOGIN_GRACE_LIMIT},
    {"A_LOGIN_GRACE_REMAINING", 0, A_LOGIN_GRACE_REMAINING},
    {"A_LOGIN_INTRUDER_ADDRESS", 0, A_LOGIN_INTRUDER_ADDRESS},
    {"A_LOGIN_INTRUDER_ATTEMPTS", 0, A_LOGIN_INTRUDER_ATTEMPTS},
    {"A_LOGIN_INTRUDER_LIMIT", 0, A_LOGIN_INTRUDER_LIMIT},
    {"A_LOGIN_INTRUDER_RESET_TIME", 0, A_LOGIN_INTRUDER_RESET_TIME},
    {"A_LOGIN_MAXIMUM_SIMULTANEOUS", 0, A_LOGIN_MAXIMUM_SIMULTANEOUS},
    {"A_LOGIN_SCRIPT", 0, A_LOGIN_SCRIPT},
    {"A_LOGIN_TIME", 0, A_LOGIN_TIME},
    {"A_LOW_RESET_TIME", 0, A_LOW_RESET_TIME},
    {"A_LOW_SYNC_INTERVAL", 0, A_LOW_SYNC_INTERVAL},
    {"A_MAILBOX_ID", 0, A_MAILBOX_ID},
    {"A_MAILBOX_LOCATION", 0, A_MAILBOX_LOCATION},
    {"A_MEMBER", 0, A_MEMBER},
    {"A_MEMORY", 0, A_MEMORY},
    {"A_MESSAGE_SERVER", 0, A_MESSAGE_SERVER},
    {"A_MESSAGE_ROUTING_GROUP", 0, A_MESSAGE_ROUTING_GROUP},
    {"A_MESSAGING_DATABASE_LOCATION", 0, A_MESSAGING_DATABASE_LOCATION},
    {"A_MESSAGING_ROUTING_GROUP", 0, A_MESSAGING_ROUTING_GROUP},
    {"A_MESSAGING_SERVER", 0, A_MESSAGING_SERVER},
    {"A_MESSAGING_SERVER_TYPE", 0, A_MESSAGING_SERVER_TYPE},
    {"A_MINIMUM_ACCOUNT_BALANCE", 0, A_MINIMUM_ACCOUNT_BALANCE},
    {"A_NETWORK_ADDRESS", 0, A_NETWORK_ADDRESS},
    {"A_NETWORK_ADDRESS_RESTRICTION", 0, A_NETWORK_ADDRESS_RESTRICTION},
    {"A_NNS_DOMAIN", 0, A_NNS_DOMAIN},
    {"A_NOTIFY", 0, A_NOTIFY},
    {"A_OBITUARY", 0, A_OBITUARY},
    {"A_ORGANIZATION_NAME", 0, A_ORGANIZATION_NAME},
    {"A_OBJECT_CLASS", 0, A_OBJECT_CLASS},
    {"A_OPERATOR", 0, A_OPERATOR},
    {"A_ORGANIZATIONAL_UNIT_NAME", 0, A_ORGANIZATIONAL_UNIT_NAME},
    {"A_OWNER", 0, A_OWNER},
    {"A_PAGE_DESCRIPTION_LANGUAGE", 0, A_PAGE_DESCRIPTION_LANGUAGE},
    {"A_PARTITION_CONTROL", 0, A_PARTITION_CONTROL},
    {"A_PARTITION_CREATION_TIME", 0, A_PARTITION_CREATION_TIME},
    {"A_PARTITION_STATUS", 0, A_PARTITION_STATUS},
    {"A_PASSWORD_ALLOW_CHANGE", 0, A_PASSWORD_ALLOW_CHANGE},
    {"A_PASSWORD_EXPIRATION_INTERVAL", 0, A_PASSWORD_EXPIRATION_INTERVAL},
    {"A_PASSWORD_EXPIRATION_TIME", 0, A_PASSWORD_EXPIRATION_TIME},
    {"A_PASSWORD_MANAGEMENT", 0, A_PASSWORD_MANAGEMENT},
    {"A_PASSWORD_MINIMUM_LENGTH", 0, A_PASSWORD_MINIMUM_LENGTH},
    {"A_PASSWORD_REQUIRED", 0, A_PASSWORD_REQUIRED},
    {"A_PASSWORD_UNIQUE_REQUIRED", 0, A_PASSWORD_UNIQUE_REQUIRED},
    {"A_PASSWORDS_USED", 0, A_PASSWORDS_USED},
    {"A_PATH", 0, A_PATH},
    {"A_PERMANENT_CONFIG_PARMS", 0, A_PERMANENT_CONFIG_PARMS},
    {"A_PHYSICAL_DELIVERY_OFFICE_NAME", 0, A_PHYSICAL_DELIVERY_OFFICE_NAME},
    {"A_POSTAL_ADDRESS", 0, A_POSTAL_ADDRESS},
    {"A_POSTAL_CODE", 0, A_POSTAL_CODE},
    {"A_POSTAL_OFFICE_BOX", 0, A_POSTAL_OFFICE_BOX},
    {"A_POSTMASTER", 0, A_POSTMASTER},
    {"A_PRINT_SERVER", 0, A_PRINT_SERVER},
    {"A_PRIVATE_KEY", 0, A_PRIVATE_KEY},
    {"A_PRINTER", 0, A_PRINTER},
    {"A_PRINTER_CONFIGURATION", 0, A_PRINTER_CONFIGURATION},
    {"A_PRINTER_CONTROL", 0, A_PRINTER_CONTROL},
    {"A_PRINT_JOB_CONFIGURATION", 0, A_PRINT_JOB_CONFIGURATION},
    {"A_PROFILE", 0, A_PROFILE},
    {"A_PROFILE_MEMBERSHIP", 0, A_PROFILE_MEMBERSHIP},
    {"A_PUBLIC_KEY", 0, A_PUBLIC_KEY},
    {"A_QUEUE", 0, A_QUEUE},
    {"A_QUEUE_DIRECTORY", 0, A_QUEUE_DIRECTORY},
    {"A_RECEIVED_UP_TO", 0, A_RECEIVED_UP_TO},
    {"A_REFERENCE", 0, A_REFERENCE},
    {"A_REPLICA", 0, A_REPLICA},
    {"A_REPLICA_UP_TO", 0, A_REPLICA_UP_TO},
    {"A_RESOURCE", 0, A_RESOURCE},
    {"A_REVISION", 0, A_REVISION},
    {"A_ROLE_OCCUPANT", 0, A_ROLE_OCCUPANT},
    {"A_STATE_OR_PROVINCE_NAME", 0, A_STATE_OR_PROVINCE_NAME},
    {"A_STREET_ADDRESS", 0, A_STREET_ADDRESS},
    {"A_SAP_NAME", 0, A_SAP_NAME},
    {"A_SECURITY_EQUALS", 0, A_SECURITY_EQUALS},
    {"A_SECURITY_FLAGS", 0, A_SECURITY_FLAGS},
    {"A_SEE_ALSO", 0, A_SEE_ALSO},
    {"A_SERIAL_NUMBER", 0, A_SERIAL_NUMBER},
    {"A_SERVER", 0, A_SERVER},
    {"A_SERVER_HOLDS", 0, A_SERVER_HOLDS},
    {"A_STATUS", 0, A_STATUS},
    {"A_SUPPORTED_CONNECTIONS", 0, A_SUPPORTED_CONNECTIONS},
    {"A_SUPPORTED_GATEWAY", 0, A_SUPPORTED_GATEWAY},
    {"A_SUPPORTED_SERVICES", 0, A_SUPPORTED_SERVICES},
    {"A_SUPPORTED_TYPEFACES", 0, A_SUPPORTED_TYPEFACES},
    {"A_SURNAME", 0, A_SURNAME},
    {"A_IN_SYNC_UP_TO", 0, A_IN_SYNC_UP_TO},
    {"A_TELEPHONE_NUMBER", 0, A_TELEPHONE_NUMBER},
    {"A_TITLE", 0, A_TITLE},
    {"A_TYPE_CREATOR_MAP", 0, A_TYPE_CREATOR_MAP},
    {"A_UID", 0, A_UID},
    {"A_UNKNOWN", 0, A_UNKNOWN},
    {"A_UNKNOWN_BASE_CLASS", 0, A_UNKNOWN_BASE_CLASS},
    {"A_USER", 0, A_USER},
    {"A_VERSION", 0, A_VERSION},
    {"A_VOLUME", 0, A_VOLUME},
    {NULL, 0}
};

PyObject *
mdb_Init(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;

    if (PyArg_ParseTuple(args, "")) {
	result = (MDBInit() ? Py_True : Py_False);
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_Shutdown(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;

    if (PyArg_ParseTuple(args, "")) {
	result = (MDBShutdown() ? Py_True : Py_False);
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_GetAPIVersion(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    int compv;
    int apiv;
    MDBHandle handle;
    PyObject *handleObj;

    if (PyArg_ParseTuple(args, "iO", &compv, &handleObj)) {
        handle = PyCObject_AsVoidPtr(handleObj);
	apiv = MDBGetAPIVersion((compv ? TRUE : FALSE), NULL, handle);
	result = Py_BuildValue("i", apiv);
    }
    return result;
}

PyObject *
mdb_GetAPIDescription(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    unsigned char desc[MDB_MAX_API_DESCRIPTION_CHARS];
    MDBHandle handle;
    PyObject *handleObj;

    if (PyArg_ParseTuple (args, "O", &handleObj)) {
        handle = PyCObject_AsVoidPtr(handleObj);
        MDBGetAPIVersion(TRUE, desc, handle);
        result = Py_BuildValue("z", desc);
    }
    return result;
}

PyObject *
mdb_GetBaseDN(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "O|O", &handleObj, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = Py_BuildValue("z", MDBGetBaseDN(vs));
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_Authenticate(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    unsigned char *module;
    unsigned char *object;
    unsigned char *password;
    MDBHandle handle;

    if (PyArg_ParseTuple(args, "szz", &module, &object, &password)) {
	handle = MDBAuthenticate(module, object,  password);
        if(handle) {
            result = PyCObject_FromVoidPtr(handle, NULL);
        } else {
            result = Py_None;
            Py_INCREF(result);
        }
    }
    return result;
}

PyObject *
mdb_Release(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;

    if (PyArg_ParseTuple(args, "O", &handleObj)) {
        handle = PyCObject_AsVoidPtr(handleObj);
	result = (MDBRelease(handle) ? Py_True : Py_False);
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_CreateValueStruct(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    MDBValueStruct *vs;
    unsigned char *context = NULL;

    if (PyArg_ParseTuple(args, "O|z", &handleObj, &context)) {
        handle = PyCObject_AsVoidPtr(handleObj);
	vs = MDBCreateValueStruct(handle, context);
	result = PyCObject_FromVoidPtr(vs, NULL);
    }
    return result;
}

PyObject *
mdb_DestroyValueStruct(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBValueStruct *vs;
    PyObject *vsObj;
    
    if (PyArg_ParseTuple(args, "O", &vsObj)) {
        vs = PyCObject_AsVoidPtr(vsObj);
	result = (MDBDestroyValueStruct(vs) ? Py_True : Py_False);
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_VSGetUsed(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBValueStruct *vs;
    PyObject *vsObj;

    if (PyArg_ParseTuple(args, "O", &vsObj)) {
        vs = PyCObject_AsVoidPtr(vsObj);
	result = Py_BuildValue("l", vs->Used);
    }
    return result;
}

PyObject *
mdb_VSGetValue(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBValueStruct *vs;
    PyObject *vsObj;
    int index;

    if (PyArg_ParseTuple(args, "Oi", &vsObj, &index)) {
        vs = PyCObject_AsVoidPtr(vsObj);
	result = Py_BuildValue("z", vs->Value[index]);
    }
    return result;
}

PyObject *
mdb_VSGetErrNo(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBValueStruct *vs;
    PyObject *vsObj;

    if (PyArg_ParseTuple(args, "O", &vsObj)) {
        vs = PyCObject_AsVoidPtr(vsObj);
	result = Py_BuildValue("l", vs->ErrNo);
    }
    return result;
}

PyObject *
mdb_VSAddValue(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBValueStruct *vs;
    PyObject *vsObj;
    unsigned char *value = NULL;

    if (PyArg_ParseTuple(args, "zO", &value, &vsObj)) {
        vs = PyCObject_AsVoidPtr(vsObj);
        result = (MDBAddValue(value, vs) ? Py_True : Py_False);
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_VSFreeValue(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBValueStruct *vs;
    PyObject *vsObj;
    unsigned long index;

    if (PyArg_ParseTuple(args, "lO", &index, &vsObj)) {
        vs = PyCObject_AsVoidPtr(vsObj);
        result = (MDBFreeValue(index, vs) ? Py_True : Py_False);
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_VSFreeValues(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBValueStruct *vs;
    PyObject *vsObj;

    if (PyArg_ParseTuple(args, "O", &vsObj)) {
        vs = PyCObject_AsVoidPtr(vsObj);
        result = (MDBFreeValues(vs) ? Py_True : Py_False);
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_VSSetContext(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    unsigned char *context;
    MDBValueStruct *vs;
    PyObject *vsObj;
    
    if (PyArg_ParseTuple(args, "zO", &context, &vsObj)) {
        vs = PyCObject_AsVoidPtr(vsObj);
        result = (MDBSetValueStructContext(context, vs) ? Py_True : Py_False);
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_ShareContext(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBValueStruct *oldvs;
    PyObject *oldvsObj;
    MDBValueStruct *newvs;
    
    if (PyArg_ParseTuple(args, "O", &oldvsObj)) {
        oldvsObj = PyCObject_AsVoidPtr(oldvsObj);
        newvs = MDBShareContext(oldvs);
        result = PyCObject_FromVoidPtr(newvs, NULL);
    }
    return result;
}

PyObject *
mdb_CreateEnumStruct(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    MDBEnumStruct *es;
    MDBHandle handle;
    PyObject *handleObj;

    if (PyArg_ParseTuple(args, "O|O", &handleObj, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            es = MDBCreateEnumStruct(vs);
            result = PyCObject_FromVoidPtr(es, NULL);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_DestroyEnumStruct(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    MDBEnumStruct *es;
    PyObject *esObj;
    MDBHandle handle;
    PyObject *handleObj;
    
    if (PyArg_ParseTuple(args, "OO|O", &handleObj, &esObj, &vsObj)) {
        es = PyCObject_AsVoidPtr(esObj);
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBDestroyEnumStruct(es, vs) ? Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_Read(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    long val;
    
    if (PyArg_ParseTuple(args, "Ozz|O", &handleObj,
                         &object, &attribute, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            val = MDBRead(object, attribute, vs);
            result = Py_BuildValue("l", val);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_ReadEx(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    MDBEnumStruct *es;
    PyObject *esObj;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    const unsigned char *val;
    
    if (PyArg_ParseTuple(args, "OzzO|O", &handleObj,
                         &object, &attribute, &esObj, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            es = PyCObject_AsVoidPtr(esObj);
            val = MDBReadEx(object, attribute, es, vs);
            result = Py_BuildValue("z", val);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_ReadDN(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    long val;
    
    if (PyArg_ParseTuple(args, "Ozz|O", &handleObj,
                         &object, &attribute, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            val = MDBReadDN(object, attribute, vs);
            result = Py_BuildValue("l", val);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_Write(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozz|O", &handleObj,
                         &object, &attribute, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBWrite(object, attribute, vs) ? Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_WriteTyped(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    int attrtype;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozzi|O", &handleObj,
                         &object, &attribute, &attrtype, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBWriteTyped(object, attribute, attrtype, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_WriteDN(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozz|O", &handleObj,
                         &object, &attribute, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBWriteDN(object, attribute, vs) ? Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_Clear(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozz|O", &handleObj,
                         &object, &attribute, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBClear(object, attribute, vs) ? Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_Add(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    unsigned char *value;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozzz|O", &handleObj,
                         &object, &attribute, &value, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBAdd(object, attribute, value, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_AddDN(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    unsigned char *value;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozzz|O", &handleObj,
                         &object, &attribute, &value, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBAddDN(object, attribute, value, vs) ? Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_Remove(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    unsigned char *value;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozzz|O", &handleObj,
                         &object, &attribute, &value, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBRemove(object, attribute, value, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_RemoveDN(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    unsigned char *value;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozzz|O", &handleObj,
                         &object, &attribute, &value, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBRemoveDN(object, attribute, value, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_IsObject(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Oz|O", &handleObj, &object, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBIsObject(object, vs) ? Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_GetObjectDetails_Type(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    unsigned char val[MDB_MAX_OBJECT_CHARS];
    
    if (PyArg_ParseTuple(args, "Oz|O", &handleObj, &object, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            if (MDBGetObjectDetails(object, val, NULL, NULL, vs)) {
                result = Py_BuildValue("z", val);
            }
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_GetObjectDetails_RDN(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    unsigned char val[MDB_MAX_OBJECT_CHARS];
    
    if (PyArg_ParseTuple(args, "Oz|O", &handleObj, &object, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            if (MDBGetObjectDetails(object, NULL, val, NULL, vs)) {
                result = Py_BuildValue("z", val);
            }
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_GetObjectDetails_DN(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    unsigned char val[MDB_MAX_OBJECT_CHARS];
    
    if (PyArg_ParseTuple(args, "Oz|O", &handleObj, &object, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            if (MDBGetObjectDetails(object, NULL, NULL, val, vs)) {
                result = Py_BuildValue("z", val);
            }
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_VerifyPassword(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *user;
    unsigned char *pass;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;

    if (PyArg_ParseTuple(args, "Ozz|O", &handleObj, &user, &pass, &vsObj)) {
        if(user == NULL || pass == NULL) {
            Py_INCREF(Py_False);
            return Py_False;
        }

        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBVerifyPassword(user, pass, vs) ? Py_True : Py_False);
        }
        if(vsObj == NULL) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_VerifyPasswordEx(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *user;
    unsigned char *pass;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;

   if (PyArg_ParseTuple(args, "Ozz|O", &handleObj, &user, &pass, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBVerifyPasswordEx(user, pass, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_ChangePassword(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *oldpass;
    unsigned char *newpass;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozzz|O", &handleObj,
                         &object, &oldpass, &newpass, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBChangePassword(object, oldpass, newpass, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_ChangePasswordEx(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *oldpass;
    unsigned char *newpass;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozzz|O", &handleObj,
                         &object, &oldpass, &newpass, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBChangePasswordEx(object, oldpass, newpass, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_EnumerateObjects(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *container;
    unsigned char *type;
    unsigned char *pattern;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    long val;
    
    if (PyArg_ParseTuple(args, "Ozzz|O", &handleObj,
                         &container, &type, &pattern, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            val = MDBEnumerateObjects(container, type, pattern, vs);
            result = Py_BuildValue("l", val);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_EnumerateObjectsEx(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *container;
    unsigned char *type;
    unsigned char *pattern;
    long flags;
    MDBEnumStruct *es;
    PyObject *esObj;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    const unsigned char *val;
    
    if (PyArg_ParseTuple(args, "OzzzlO|O", &handleObj, &container,
                         &type, &pattern, &flags, &esObj, &vsObj)) {
        if(vsObj == Py_None) {
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            es = PyCObject_AsVoidPtr(esObj);
            val = MDBEnumerateObjectsEx(container, type, pattern, flags,
                                        es, vs);
            result = Py_BuildValue("z", val);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_EnumerateAttributesEx(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    MDBEnumStruct *es;
    PyObject *esObj;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    const unsigned char *val;
    
    if (PyArg_ParseTuple(args, "OzO|O", &handleObj, &object, &esObj, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            es = PyCObject_AsVoidPtr(esObj);
            val = MDBEnumerateAttributesEx(object, es, vs);
            result = Py_BuildValue("z", val);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
   }
    return result;
}

PyObject *
mdb_CreateObject(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *class;
    MDBValueStruct *vsattr;
    PyObject *vsattrObj;
    MDBValueStruct *vsdata;
    PyObject *vsdataObj;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "OzzOO|O", &handleObj,
                         &object, &class, &vsattrObj, &vsdataObj, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            vsattr = PyCObject_AsVoidPtr(vsattrObj);
            vsdata = PyCObject_AsVoidPtr(vsdataObj);
            result = (MDBCreateObject(object, class, vsattr, vsdata, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_DeleteObject(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    int recursive;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozi|O", &handleObj,
                         &object, &recursive, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBDeleteObject(object, (recursive ? TRUE : FALSE), vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_RenameObject(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *oldobject;
    unsigned char *newobject;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozz|O", &handleObj,
                         &oldobject, &newobject, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBRenameObject(oldobject, newobject, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_DefineAttribute(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *attribute;
    unsigned char *asn1;
    long type;
    int singleval;
    int immediatesync;
    int public;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozzliii|O", &handleObj, &attribute, &asn1,
                         &type, &singleval, &immediatesync, &public, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBDefineAttribute(attribute, asn1, type,
                                         (singleval ? TRUE : FALSE),
                                         (immediatesync ? TRUE : FALSE),
                                         (public ? TRUE : FALSE), vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_UndefineAttribute(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *attribute;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Oz|O", &handleObj, &attribute, &vs)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBUndefineAttribute(attribute, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_AddAttribute(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *attribute;
    unsigned char *class;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozz|O", &handleObj,
                         &attribute, &class, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBAddAttribute(attribute, class, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_DefineClass(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *class;
    unsigned char *asn1;
    int container;
    MDBValueStruct *superclass;
    PyObject *superclassObj;
    MDBValueStruct *containment;
    PyObject *containmentObj;
    MDBValueStruct *naming;
    PyObject *namingObj;
    MDBValueStruct *mandatory;
    PyObject *mandatoryObj;
    MDBValueStruct *optional;
    PyObject *optionalObj;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "OzziOOOOO|O", &handleObj,
                         &class, &asn1, &container,
                         &superclassObj, &containmentObj, &namingObj,
                         &mandatoryObj, &optionalObj, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            superclass = PyCObject_AsVoidPtr(superclassObj);
            containment = PyCObject_AsVoidPtr(containmentObj);
            naming = PyCObject_AsVoidPtr(namingObj);
            mandatory = PyCObject_AsVoidPtr(mandatoryObj);
            optional = PyCObject_AsVoidPtr(optionalObj);
            result = (MDBDefineClass(class, asn1, (container ? TRUE : FALSE),
                                     superclass, containment, naming,
                                     mandatory, optional, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_UndefineClass(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *class;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Oz|O", &handleObj, &class, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBUndefineClass(class, vs) ? Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_ListContainableClasses(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *container;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Oz|O", &handleObj, &container, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBListContainableClasses(container, vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_ListContainableClassesEx(PyObject *self, PyObject *args)
{
    PyObject *result = NULL;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *container;
    MDBEnumStruct *es;
    PyObject *esObj;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    const unsigned char* val = NULL;
    
    if (PyArg_ParseTuple(args, "OzO|O", &container, &esObj, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            es = PyCObject_AsVoidPtr(esObj);
            val = MDBListContainableClassesEx(container, es, vs);
            result = Py_BuildValue("z", val);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    return result;
}

PyObject *
mdb_GrantObjectRights(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *trusteedn;
    int read;
    int write;
    int delete;
    int rename; 
    int admin;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozziiiii|O", &handleObj, &object, &trusteedn,
                         &read, &write, &delete, &rename, &admin, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBGrantObjectRights(object, trusteedn,
                                           (read ? TRUE : FALSE),
                                           (write ? TRUE : FALSE),
                                           (delete ? TRUE : FALSE),
                                           (rename ? TRUE : FALSE),
                                           (admin ? TRUE : FALSE), vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_GrantAttributeRights(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBHandle handle;
    PyObject *handleObj;
    unsigned char *object;
    unsigned char *attribute;
    unsigned char *trusteedn;
    int read;
    int write;
    int admin;
    MDBValueStruct *vs;
    PyObject *vsObj = Py_None;
    
    if (PyArg_ParseTuple(args, "Ozzziii|O", &handleObj,
                         &object, &attribute, &trusteedn,
                         &read, &write, &admin, &vsObj)) {
        if(vsObj == Py_None) {
            handle = PyCObject_AsVoidPtr(handleObj);
            vs = MDBCreateValueStruct(handle, NULL);
        } else {
            vs = PyCObject_AsVoidPtr(vsObj);
        }
        if(vs) {
            result = (MDBGrantAttributeRights(object, attribute, trusteedn,
                                              (read ? TRUE : FALSE),
                                              (write ? TRUE : FALSE),
                                              (admin ? TRUE : FALSE), vs) ?
                      Py_True : Py_False);
        }
        if(vsObj == Py_None) {
            MDBDestroyValueStruct(vs);
        }
    }
    Py_INCREF(result);
    return result;
}

PyObject *
mdb_DumpValueStruct(PyObject *self, PyObject *args)
{
    PyObject *result = Py_None;
    MDBValueStruct *vs;
    PyObject *vsObj;
    
    if (PyArg_ParseTuple(args, "O", &vsObj)) {
        vs = PyCObject_AsVoidPtr(vsObj);
        result = (MDBDumpValueStruct(vs) ? Py_True : Py_False);
    }
    Py_INCREF(result);
    return result;
}

#ifdef __cplusplus
}
#endif
