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
#include <bongoutil.h>
#include <string.h>
#include "libs.h"

#ifdef __cplusplus
extern "C" {
#endif


PyDoc_STRVAR(ModifiedUtf7ToUtf8_doc,
"ModifiedUtf7ToUtf8(s) -> string response\n\
\n\
Converts modified UTF-7 as described in IMAP (RFC 3501) into UTF-8.\n");

static PyObject *
bongoutil_ModifiedUtf7ToUtf8(PyObject *self, PyObject *args)
{
    char *utf7;
    unsigned int utf7Len;
    char utf8[XPL_MAX_PATH];
    unsigned int utf8Len;

    if (!PyArg_ParseTuple(args, "s", &utf7)) {
        return NULL;
    }

    utf7Len = strlen(utf7);

    utf8Len = ModifiedUtf7ToUtf8(utf7, utf7Len, utf8, XPL_MAX_PATH);
    if(utf8Len < 0) {
       Py_INCREF(Py_None);
       return Py_None;
    }

    utf8[utf8Len] = 0;

    return Py_BuildValue("s", utf8);
}


PyMethodDef BongoUtilMethods[] = {
    {"ModifiedUtf7ToUtf8", bongoutil_ModifiedUtf7ToUtf8, METH_VARARGS | METH_STATIC, ModifiedUtf7ToUtf8_doc},
    {NULL, NULL, 0, NULL}
};

#ifdef __cplusplus
}
#endif
