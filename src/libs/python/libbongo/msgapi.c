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

#include <bongo-config.h>
#include <xpl.h>
#include <msgapi.h>
#include <connio.h>
#include "libs.h"
#include "msgapi-defs.h"

#ifdef __cplusplus
extern "C" {
#endif


PyDoc_STRVAR(CollectAllUsers_doc,
"CollectAllUsers() -> None\n\
\n\
Collect and import the calendars subscribed to by all users.");

static PyObject *
msgapi_CollectAllUsers(PyObject *self, PyObject *args)
{
    if (!PyArg_ParseTuple(args, "")) {
        return NULL;
    }

    MsgCollectAllUsers();

    Py_INCREF(Py_None);
    return Py_None;
}

PyDoc_STRVAR(CollectUser_doc,
"CollectUser(user) -> None\n\
\n\
Collect and import the calendars subscribed to by user.");

static PyObject *
msgapi_CollectUser(PyObject *self, PyObject *args)
{
    char *user;

    if (!PyArg_ParseTuple(args, "s", &user)) {
        return NULL;
    }

    MsgCollectUser(user);

    Py_INCREF(Py_None);
    return Py_None;
}

PyDoc_STRVAR(DirectoryHandle_doc,
"DirectoryHandle() -> handle\n\
\n\
Return the directory handle used by libmsgapi.");

static PyObject *
msgapi_DirectoryHandle(PyObject *self, PyObject *args)
{
    MDBHandle handle;

    if (!PyArg_ParseTuple(args, "")) {
        return NULL;
    }

    handle = (MDBHandle)MsgDirectoryHandle();

    if (handle == NULL) {
        Py_INCREF(Py_None);
        return Py_None;
    }

    return PyCObject_FromVoidPtr(handle, NULL);
}

PyDoc_STRVAR(FindObject_doc,
"FindObject(u) -> string dn\n\
\n\
Return a string containing the dn of user u.");

static PyObject *
msgapi_FindObject(PyObject *self, PyObject *args)
{
    unsigned char *user;
    unsigned int len=MDB_MAX_OBJECT_CHARS+1;
    unsigned char buf[len];

    if (!PyArg_ParseTuple(args, "s", &user)) {
        return NULL;
    }

    if (!MsgFindObject(user, buf, NULL, NULL, NULL)) {
        Py_INCREF(Py_None);
        return Py_None;
    }

    return Py_BuildValue("s", buf);
}

PyDoc_STRVAR(GetUserFeature_doc,
"GetUserFeature(dn, featurename, ) -> array of strings\n\
\n\
Return a string containing the ip address of user u's document store.");

static const char *features[] = {
    "",
    "imap",
    "pop",
    "addressbook",
    "proxy",
    "forward",
    "autoreply",
    "rules",
    "finger",
    "smtp_send_count_limit",
    "smtp_send_size_limit",
    "nmap_quota",
    "nmap_store",
    "webmail",
    "modweb",
    "mwmail_addressbook_personal",
    "mwmail_addressbook_system",
    "mwmail_addressbook_global",
    "modweb_wap",
    "calagent",
    "calendar",
    "antivirus",
    "shared_folders",
    NULL
};

static void
ThrowImportException(int code)
{
    char *msg;
    switch(code) {
    case MSG_COLLECT_ERROR_URL :
        msg = "Invalid URL";
        break;
    case MSG_COLLECT_ERROR_DOWNLOAD :
        msg = "Unable to download calendar";
        break;
    case MSG_COLLECT_ERROR_AUTH :
        msg = "Invalid username and password";
        break;
    case MSG_COLLECT_ERROR_PARSE :
        msg = "Unable to parse calendar";
        break;
    case MSG_COLLECT_ERROR_STORE :
        msg = "Error saving calendar";
        break;
    default :
        msg = "Error importing calendar";
        break;
    }

    PyErr_SetString(PyExc_RuntimeError, msg);
}

PyDoc_STRVAR(ImportIcsUrl_doc,
"ImportIcs(user, calName, url, urlUsername, urlPassword)\n\
\n\
Import the specified url into the calendar calName.\n\
The calendar with that name must already exist.");

static PyObject *
msgapi_ImportIcsUrl(PyObject *self, PyObject *args)
{
    char *user;
    char *calName;
    char *url;
    char *urlUsername;
    char *urlPassword;
    char *color;
    int ret;

    if (!PyArg_ParseTuple(args, "sszszz", &user, &calName, &color, &url, &urlUsername, &urlPassword)) {
        return NULL;
    }

    ret = MsgImportIcsUrl(user, calName, color, url, urlUsername, urlPassword, NULL);
    if (ret < 0) {
        ThrowImportException(ret);
        return NULL;
    }
    Py_INCREF(Py_None);
    return Py_None;
}

PyDoc_STRVAR(ImportIcs_doc,
"ImportIcs(fileHandle, user, calName, color)\n\
\n\
Import from the specified open file into the calendar calName.\n\
The calendar with that name must already exist.");

static PyObject *
msgapi_ImportIcs(PyObject *self, PyObject *args)
{
    PyObject *pyfile = NULL;
    FILE *file = NULL;
    char *user;
    char *calName;
    char *color;
    int ret;

    if (!PyArg_ParseTuple(args, "Ossz", &pyfile, &user, &calName, &color)) {
        return NULL;
    }
    if(!PyFile_Check(pyfile))
        return NULL;

    file = PyFile_AsFile(pyfile);

    ret = MsgImportIcs(file, user, calName, color, NULL);
    if (ret < 0) {
        ThrowImportException(ret);
        return NULL;
    }
    Py_INCREF(Py_None);
    return Py_None;
}

PyDoc_STRVAR(IcsImport_doc,
"IcsImport(user, name, color, url, urlUsername, urlPassword) -> guid\n\
\n\
Import the specified calendar url.");

static PyObject *
msgapi_IcsImport(PyObject *self, PyObject *args)
{
    char *user;
    char *name;
    char *url;
    char *urlUsername;
    char *urlPassword;
    char *color;
    int ret;
    uint64_t guid;

    if (!PyArg_ParseTuple(args, "sszszz", &user, &name, &color, &url, &urlUsername, &urlPassword)) {
        return NULL;
    }

    ret = MsgIcsImport(user, name, color, url, urlUsername, urlPassword, &guid);

    if (ret < 0) {
        ThrowImportException(ret);
        return NULL;
    }

    return PyLong_FromUnsignedLong(guid);
}

PyDoc_STRVAR(IcsSubscribe_doc,
"IcsSubscribe(user, name, color, url, urlUsername, urlPassword) -> guid\n\
\n\
Subscribe user to the specified calendar url.");

static PyObject *
msgapi_IcsSubscribe(PyObject *self, PyObject *args)
{
    char *user;
    char *name;
    char *url;
    char *urlUsername;
    char *urlPassword;
    char *color;
    uint64_t guid;
    int ret;

    if (!PyArg_ParseTuple(args, "sszszz", &user, &name, &color, &url, &urlUsername, &urlPassword)) {
        return NULL;
    }

    ret = MsgIcsSubscribe(user, name, color, url, urlUsername, urlPassword, &guid);
    
    if (ret < 0) {
        ThrowImportException(ret);
        return NULL;
    }

    return PyLong_FromUnsignedLong(guid);
}

PyDoc_STRVAR(NmapChallenge_doc,
"NmapChallenge(s) -> string response\n\
\n\
Return the response string for an NMAP challenge string s.\n\
s is of the format \"NMAP <40c0cbb0hostname441444a4>\".");

static PyObject *
msgapi_NmapChallenge(PyObject *self, PyObject *args)
{
    unsigned char *salt;
    unsigned int len=33;
    unsigned char buf[len];

    if (!PyArg_ParseTuple(args, "s", &salt)) {
        return NULL;
    }

    MsgNmapChallenge(salt, buf, len);

    return Py_BuildValue("s", buf);
}

PyDoc_STRVAR(GetBuildVersion_doc,
"GetBuildVersion() -> (integer, boolean)\n \
\n\
Returns a tuple containing version number and whether the build is custom.\n\
");

static PyObject *
msgapi_GetBuildVersion(PyObject *self, PyObject *args)
{
    int version;
    BOOL custom;

    MsgGetBuildVersion (&version, &custom);

    return Py_BuildValue("ib", version, custom);
}

PyDoc_STRVAR(GetAvailableVersion_doc,
"GetBuildVersion() -> integer response\n \
\n\
Returns the version of the latest software on this branch.\n\
");

static PyObject *
msgapi_GetAvailableVersion(PyObject *self, PyObject *args)
{
    int version;

    if (MsgGetAvailableVersion (&version)) {
        return Py_BuildValue("i", version);
    } else {
        return Py_BuildValue("i", 0);
    }
}

extern PyObject *msgapi_GetConfigProperty(PyObject *, PyObject *);

PyMethodDef MsgApiMethods[] = {
    {"CollectAllUsers", msgapi_CollectAllUsers, METH_VARARGS | METH_STATIC, CollectAllUsers_doc},
    {"CollectUser", msgapi_CollectUser, METH_VARARGS | METH_STATIC, CollectUser_doc},
    {"DirectoryHandle", msgapi_DirectoryHandle, METH_VARARGS | METH_STATIC, DirectoryHandle_doc},
    {"FindObject", msgapi_FindObject, METH_VARARGS | METH_STATIC, FindObject_doc},
    {"ImportIcsUrl", msgapi_ImportIcsUrl, METH_VARARGS | METH_STATIC, ImportIcsUrl_doc},
    {"ImportIcs", msgapi_ImportIcs, METH_VARARGS | METH_STATIC, ImportIcs_doc},
    {"IcsSubscribe", msgapi_IcsSubscribe, METH_VARARGS | METH_STATIC, IcsSubscribe_doc},
    {"IcsImport", msgapi_IcsImport, METH_VARARGS | METH_STATIC, IcsImport_doc},
    {"NmapChallenge", msgapi_NmapChallenge, METH_VARARGS | METH_STATIC, NmapChallenge_doc},
    {"GetConfigProperty", msgapi_GetConfigProperty, METH_VARARGS | METH_STATIC, GetConfigProperty_doc},
    {"GetUnprivilegedUser", msgapi_GetUnprivilegedUser, METH_VARARGS | METH_STATIC, GetUnprivilegedUser_doc},
    {"GetBuildVersion", msgapi_GetBuildVersion, METH_VARARGS | METH_STATIC, GetBuildVersion_doc},
    {"GetAvailableVersion", msgapi_GetAvailableVersion, METH_VARARGS | METH_STATIC, GetAvailableVersion_doc},
    {NULL, NULL, 0, NULL}
};

#ifdef __cplusplus
}
#endif
