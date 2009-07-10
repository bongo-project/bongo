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

/*
 * The bootstrap library is a very stripped-down version of "libs".
 * For bootstrapping the directory  or doing other tasks before Bongo has
 * been set up, this library (which does only minimal initialization) may
 * be imported instead of the full "libs".
 */

#include <Python.h>
#include "bootstrap.h"
#include "msgapi-defs.h"
#include <memmgr.h>
#include <msgapi.h>

#ifdef __cplusplus
extern "C" {
#endif

static PyMethodDef ModuleMethods[] = { {NULL} };
extern EnumItemDef MdbEnums[];
extern PyMethodDef MdbMethods[];
extern EnumItemDef MsgApiEnums[];
PyMethodDef MsgApiMethods[] = {
    {"GetConfigProperty", msgapi_GetConfigProperty,
     METH_VARARGS | METH_STATIC, GetConfigProperty_doc},
    {"GetUnprivilegedUser", msgapi_GetUnprivilegedUser,
     METH_VARARGS | METH_STATIC, GetUnprivilegedUser_doc},
    {NULL, NULL, 0, NULL}
};

PyMODINIT_FUNC
initbootstrap()
{
    char dbfdir[PATH_MAX];

    /* Initialize the various bongo libraries */
    if (!MemoryManagerOpen((unsigned char*)"pybongo")) {
        PyErr_SetString(PyExc_ImportError,
                        "bongo.libs error: MemoryManagerOpen() failed");
        return;
    }

    /* Create the libs module */
    PyObject *module = Py_InitModule("bootstrap", ModuleMethods);

    /* Add the Bongo libs */
    AddLibrary(module, "msgapi", MsgApiMethods, MsgApiEnums);
}

#ifdef __cplusplus
}
#endif
