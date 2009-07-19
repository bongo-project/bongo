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
#include "libs.h"
#include "pybongo.h"
#include <xpl.h>
#include <connio.h>
#include <memmgr.h>
#include <msgapi.h>
#include <nmlib.h>
#include <streamio.h>
#include <bongojson.h>

#ifdef __cplusplus
extern "C" {
#endif

static PyMethodDef ModuleMethods[] = { {NULL} };

/* Pull in the methods from the bindings */
extern PyMethodDef CalCmdMethods[];
extern EnumItemDef CalCmdEnums[];
extern PyMethodDef CalMethods[];
extern PyMethodDef MdbMethods[];
extern EnumItemDef MdbEnums[];
extern PyMethodDef MsgApiMethods[];
extern EnumItemDef MsgApiEnums[];
extern PyMethodDef StreamIOMethods[];
extern PyMethodDef BongoJsonMethods[];
extern PyMethodDef BongoUtilMethods[];
extern PyMethodDef TestMethods[];

PyMODINIT_FUNC
initlibs()
{
    char dbfdir[PATH_MAX];

    /* Initialize the various bongo libraries */
    if (!MemoryManagerOpen((unsigned char*)"pybongo")) {
        PyErr_SetString(PyExc_ImportError,
                        "bongo.libs error: MemoryManagerOpen() failed");
        return;
    }

    if (!ConnStartup(DEFAULT_CONNECTION_TIMEOUT)) {
        PyErr_SetString(PyExc_ImportError,
                        "bongo.libs error: ConnStartup() failed");
        return;
    }

    MsgInit();

    if (!BongoCalInit(MsgGetDir(MSGAPI_DIR_DBF, NULL, 0))) {
        PyErr_SetString(PyExc_ImportError,
                        "bongo.libs error: BongoCalInit() failed");
        return;
    }

    if (!NMAPInitialize()) {
        PyErr_SetString(PyExc_ImportError,
                        "bongo.libs error: NMAPInitialize() failed");
        return;
    }

    if (!StreamioInit()) {
        PyErr_SetString(PyExc_ImportError,
                        "bongo.libs error: StreamioInit() failed");
        return;
    }

    if (!BongoJsonPreInit()) {
        PyErr_SetString(PyExc_ImportError,
                        "bongo.libs error: BongoJsonPreInit() failed");
        return;
    }
    
    /* Create the libs module */
    PyObject *module = Py_InitModule("libs", ModuleMethods);

    /* Add the Bongo libs */
    AddLibrary(module, "cal", CalMethods, NULL);
    AddLibrary(module, "calcmd", CalCmdMethods, CalCmdEnums);
    AddLibrary(module, "msgapi", MsgApiMethods, NULL);
    AddLibrary(module, "streamio", StreamIOMethods, NULL);
    AddLibrary(module, "bongojson", BongoJsonMethods, NULL);
    AddLibrary(module, "bongoutil", BongoUtilMethods, NULL);
    AddLibrary(module, "tests", TestMethods, NULL);

    BongoJsonPostInit(module);
    BongoCalPostInit(module);
}

#ifdef __cplusplus
}
#endif
