#include <Python.h>
#include "structmember.h"

#include <bongojson.h>
#include <bongocal.h>

#include "libs.h"

XPL_BEGIN_C_LINKAGE

static PyMemberDef JsonObject_members[] = {
    {NULL}
};

static PyMethodDef JsonObject_methods[] = {
    {NULL}
};

static PyMappingMethods JsonObject_mapping = {
    JsonObject_length,
    JsonObject_subscript,
    JsonObject_subscript_assign
};

PyTypeObject JsonObjectType = {
    PyObject_HEAD_INIT(NULL)
    0, /* ob_size */
    "libs.JsonObject",
    sizeof(JsonObject),
    0,                         /*tp_itemsize*/
    (destructor)JsonObject_dealloc, /*tp_dealloc*/
    0,                         /*tp_print*/
    0,                         /*tp_getattr*/
    0,                         /*tp_setattr*/
    0,                         /*tp_compare*/
    0,                         /*tp_repr*/
    0,                         /*tp_as_number*/
    0,                         /*tp_as_sequence*/
    &JsonObject_mapping,       /*tp_as_mapping*/
    0,                         /*tp_hash */
    0,                         /*tp_call*/
    JsonObject_str,            /*tp_str*/
    0,                         /*tp_getattro*/
    0,                         /*tp_setattro*/
    0,                         /*tp_as_buffer*/
    Py_TPFLAGS_DEFAULT | Py_TPFLAGS_BASETYPE,        /*tp_flags*/
    "Bongo Json Object",           /* tp_doc */
    0,		               /* tp_traverse */
    0,		               /* tp_clear */
    0,		               /* tp_richcompare */
    0,		               /* tp_weaklistoffset */
    0,		               /* tp_iter */
    0,		               /* tp_iternext */
    JsonObject_methods,        /* tp_methods */
    JsonObject_members,        /* tp_members */
    0,                         /* tp_getset */
    0,                         /* tp_base */
    0,                         /* tp_dict */
    0,                         /* tp_descr_get */
    0,                         /* tp_descr_set */
    0,                         /* tp_dictoffset */
    (initproc)JsonObject_init, /* tp_init */
    0,                         /* tp_alloc */
    JsonObject_new,            /* tp_new */
};


static PyMemberDef JsonArray_members[] = {
    {NULL}
};

static PyMethodDef JsonArray_methods[] = {
    {"append", JsonArray_append, METH_VARARGS, "Add a json object"},
    {NULL}
};

static PySequenceMethods JsonArray_sequence = {
    JsonArray_length,
    JsonArray_concat,
    JsonArray_repeat,
    JsonArray_item,
    JsonArray_slice,
    JsonArray_assign,
    JsonArray_assign_slice,
    JsonArray_contains,
    JsonArray_inplace_concat,
    JsonArray_inplace_repeat,
};

#if 0
    lenfunc sq_length;
    binaryfunc sq_concat;
    ssizeargfunc sq_repeat;
    ssizeargfunc sq_item;
    ssizessizeargfunc sq_slice;
    ssizeobjargproc sq_ass_item;
    ssizessizeobjargproc sq_ass_slice;
    objobjproc sq_contains;
    /* Added in release 2.0 */
    binaryfunc sq_inplace_concat;
    ssizeargfunc sq_inplace_repeat;
#endif


PyTypeObject JsonArrayType = {
    PyObject_HEAD_INIT(NULL)
    0, /* ob_size */
    "libs.JsonArray",
    sizeof(JsonArray),
    0,                         /*tp_itemsize*/
    (destructor)JsonArray_dealloc, /*tp_dealloc*/
    0,                         /*tp_print*/
    0,                         /*tp_getattr*/
    0,                         /*tp_setattr*/
    0,                         /*tp_compare*/
    0,                         /*tp_repr*/
    0,                         /*tp_as_number*/
    &JsonArray_sequence,        /*tp_as_sequence*/
    0,                         /*tp_as_mapping*/
    0,                         /*tp_hash */
    0,                         /*tp_call*/
    JsonArray_str,             /*tp_str*/
    0,                         /*tp_getattro*/
    0,                         /*tp_setattro*/
    0,                         /*tp_as_buffer*/
    Py_TPFLAGS_DEFAULT | Py_TPFLAGS_BASETYPE,        /*tp_flags*/
    "Bongo Json Array",         /* tp_doc */
    0,		               /* tp_traverse */
    0,		               /* tp_clear */
    0,		               /* tp_richcompare */
    0,		               /* tp_weaklistoffset */
    0,		               /* tp_iter */
    0,		               /* tp_iternext */
    JsonArray_methods,         /* tp_methods */
    JsonArray_members,         /* tp_members */
    0,                         /* tp_getset */
    0,                         /* tp_base */
    0,                         /* tp_dict */
    0,                         /* tp_descr_get */
    0,                         /* tp_descr_set */
    0,                         /* tp_dictoffset */
    (initproc)JsonArray_init,  /* tp_init */
    0,                         /* tp_alloc */
    JsonArray_new,             /* tp_new */
};

static void
RaiseJsonError(BongoJsonResult res)
{
    PyErr_Format(PyExc_ValueError,  "Error parsing json: %d", res);
}

static BongoJsonNode *
JsonPyToNode(PyObject *obj)
{
    BongoJsonNode *node;

    if (obj == Py_None) {
        return BongoJsonNodeNewNull();
    } else if (PyBool_Check(obj)) {
        if (obj == Py_False) {
            node = BongoJsonNodeNewBool(FALSE);
        } else {
            node = BongoJsonNodeNewBool(TRUE);
        }
    } else if (PyInt_Check(obj)) {
        node = BongoJsonNodeNewInt(PyInt_AsLong(obj));
    } else if (PyLong_Check(obj)) {
        node = BongoJsonNodeNewInt(PyLong_AsLong(obj));
    } else if (PyFloat_Check(obj)) {
        node = BongoJsonNodeNewDouble(PyFloat_AsDouble(obj));
    } else if (PyString_Check(obj)) {
        node = BongoJsonNodeNewString(PyString_AsString(obj));
    } else if (PyUnicode_Check(obj)) {
        PyObject *utf8 = PyUnicode_AsUTF8String(obj);
        node = BongoJsonNodeNewString(PyString_AsString(utf8));
        Py_DECREF(utf8);
    } else if (PyList_Check(obj)) {
        GArray *array;
        int len = PyList_Size(obj);
        int i;

        array = g_array_sized_new(FALSE, FALSE, sizeof(BongoJsonNode*), len);

        for (i=0; i<len; i++) {
            BongoJsonNode *item = JsonPyToNode(PyList_GetItem(obj, i));
            g_array_append_val(array, item);
        }

        node = BongoJsonNodeNewArray(array);
    } else if (PyObject_IsInstance(obj, (PyObject*)&JsonObjectType)) {
        JsonObject *json = ((JsonObject*)obj);
        node = BongoJsonNodeNewObject(json->obj);
        json->own = FALSE;
    } else if (PyObject_IsInstance(obj, (PyObject*)&JsonArrayType)) {
        JsonArray *json = ((JsonArray*)obj);
        node = BongoJsonNodeNewArray(json->array);
        json->own = FALSE;
    } else {
        node = NULL;
    }
    
    if (node == NULL) {
        PyErr_SetString(PyExc_TypeError, "Creating JSON node from unsupported type");
    }

    return node;
}

PyObject *
JsonObjectToPy(BongoJsonObject *object, BOOL own)
{
    PyObject *ret = (PyObject*)PyObject_New(JsonObject, &JsonObjectType);
    ((JsonObject*)ret)->obj = object;
    ((JsonObject*)ret)->own = own;
    ((JsonObject*)ret)->valid = TRUE;

    return ret;
}            

static PyObject *
JsonObjectToPyNative(BongoJsonObject *obj)
{
    PyObject *ret;
    
    ret = PyDict_New();
    
    BongoJsonObjectIter iter;
    for (BongoJsonObjectIterFirst(obj, &iter);
         iter.key != NULL;
         BongoJsonObjectIterNext(obj, &iter)) {
        PyObject *child;
        child = JsonNodeToPy(iter.value, FALSE, TRUE);
        if (!child) {
            Py_DECREF(ret);
            return NULL;
        }
        if (PyDict_SetItemString (ret, iter.key, child) == -1) {
            Py_DECREF(ret);
            return NULL;
        }
    }

    return ret;
}

static PyObject *
JsonArrayToPyNative(GArray *array)
{
    PyObject *ret;
    unsigned int i;
    
    ret = PyList_New(0);
    
    for (i = 0; i < array->len; i++) {
        PyObject *child;
        child = JsonNodeToPy (BongoJsonArrayGet(array, i), FALSE, TRUE);
        if (!child) {
            Py_DECREF(ret);
            return NULL;
        }
        
        if (PyList_Append (ret, child) == -1) {
            Py_DECREF(ret);
            return NULL;
        }
    }

    return ret;
}


PyObject *
JsonNodeToPy(BongoJsonNode *node, BOOL own, BOOL native)
{
    PyObject *ret;
    char *str;
    
    switch(node->type) {
    case BONGO_JSON_OBJECT :
        if (native) {
            return JsonObjectToPyNative(BongoJsonNodeAsObject(node));
        } else {
            ret = JsonObjectToPy(BongoJsonNodeAsObject(node), own);
        }
        break;
    case BONGO_JSON_ARRAY :
        if (native) {
            return JsonArrayToPyNative(BongoJsonNodeAsArray (node));
        } else {
            ret = (PyObject*)PyObject_New(JsonArray, &JsonArrayType);
            ((JsonArray*)ret)->own = own;
            ((JsonArray*)ret)->array = BongoJsonNodeAsArray(node);
        }        
    case BONGO_JSON_BOOL :
        if (BongoJsonNodeAsBool(node)) {
            ret = Py_True;
        } else {
            ret = Py_False;
        }
        Py_INCREF(ret);
        break;
    case BONGO_JSON_DOUBLE :
        ret = Py_BuildValue("d", BongoJsonNodeAsDouble(node));
        break;
    case BONGO_JSON_INT :
        ret = Py_BuildValue("i", BongoJsonNodeAsInt(node));
        break;
    case BONGO_JSON_STRING :
        str = BongoJsonNodeAsString(node);
        ret = PyUnicode_DecodeUTF8(str, strlen(str), NULL);
        break;
    default :
        PyErr_Format(PyExc_ValueError,  "Unknown node type: %d", node->type);
        ret = NULL;
    }

    return ret;
}

/*** JsonArray ***/

void
JsonArray_dealloc(JsonArray *self)
{
    if (self->own) {
        BongoJsonArrayFree(self->array);
    }

    self->ob_type->tp_free((PyObject*)self);
}

PyObject *
JsonArray_new(PyTypeObject *type, PyObject *args, PyObject *kwds)
{
    JsonArray *self;
    
    self = (JsonArray*)type->tp_alloc(type, 0);
    self->own = FALSE;
    self->array = NULL;

    /* JsonArray is defined as a c subclass of the PyObject.
     * i should be able to cast here safely because i'm casting
     * a pointer either way */
    return (PyObject *)self;
}

int
JsonArray_init(JsonArray *self, PyObject *args, PyObject *kwds)
{
    self->own = TRUE;
    self->array = g_array_sized_new(FALSE, FALSE, sizeof(BongoJsonNode*), 16);
    return 0;
}


PyObject *
JsonArray_append(PyObject *selfp, PyObject *args)
{
    JsonArray *self = (JsonArray*)selfp;
    PyObject *obj;
    BongoJsonNode *node;
    
    if (!PyArg_ParseTuple(args, "O", &obj)) {
        PyErr_SetString(PyExc_TypeError, "append() takes 1 argument");
        return NULL;
    }

    node = JsonPyToNode(obj);
    if (!node) {
        return NULL;
    }
    
    BongoJsonArrayAppend(self->array, node);

    Py_INCREF(Py_None);
    return Py_None;
}

PyObject *
JsonArray_str(PyObject *selfp)
{
    JsonArray *self = (JsonArray *)selfp;
    PyObject *ret;
    char *str;
    
    str = BongoJsonArrayToString(self->array);

    ret = Py_BuildValue("s", str);

    MemFree(str);

    return ret;
}

Py_ssize_t
JsonArray_length(PyObject *selfp)
{
    JsonArray *self = (JsonArray *)selfp;
    return (self->array->len > 0) ? (Py_ssize_t)self->array->len : 0;
}

PyObject *
JsonArray_concat(PyObject *selfp, PyObject *b)
{
    PyErr_SetString(PyExc_NotImplementedError, "Not implemented");
    return NULL;
}

PyObject *
JsonArray_repeat(PyObject *selfp, Py_ssize_t i)
{
    PyErr_SetString(PyExc_NotImplementedError, "Not implemented");
    return NULL;
}

PyObject *
JsonArray_item(PyObject *selfp, Py_ssize_t i)
{
    JsonArray *self = (JsonArray *)selfp;
    BongoJsonNode *node;
    
    if (i < 0 || i >= self->array->len) {
        PyErr_SetString(PyExc_IndexError, "Out of bounds");
        return NULL;
    }

    node = BongoJsonArrayGet(self->array, i);

    return JsonNodeToPy(node, FALSE, FALSE);
}

PyObject *
JsonArray_slice(PyObject *selfp, Py_ssize_t i1, Py_ssize_t i2)
{
    PyErr_SetString(PyExc_NotImplementedError, "Not implemented");
    return NULL;
}

int 
JsonArray_assign(PyObject *selfp, Py_ssize_t i, PyObject *obj)
{
    PyErr_SetString(PyExc_NotImplementedError, "Not implemented");
    return 0;
}

int 
JsonArray_assign_slice(PyObject *selfp, Py_ssize_t i1, Py_ssize_t i2, PyObject *obj)
{
    PyErr_SetString(PyExc_NotImplementedError, "Not implemented");
    return 0;
}

int 
JsonArray_contains(PyObject *selfp, PyObject *b)
{
    PyErr_SetString(PyExc_NotImplementedError, "Not implemented");
    return 0;
}

PyObject *
JsonArray_inplace_concat(PyObject *selfp, PyObject *b)
{
    PyErr_SetString(PyExc_NotImplementedError, "Not implemented");
    return NULL;
}

PyObject *
JsonArray_inplace_repeat(PyObject *selfp, Py_ssize_t i)
{
    PyErr_SetString(PyExc_NotImplementedError, "Not implemented");
    return NULL;
}

/*** JsonObject ***/

void
JsonObject_dealloc(JsonObject *self)
{
#if 0
    if (self->obj) {
        BongoJsonObjectFree(self->obj);
    }
#endif
    
    self->ob_type->tp_free((PyObject*)self);
}


PyObject *
JsonObject_new(PyTypeObject *type, PyObject *args, PyObject *kwds)
{
    JsonObject *self;

    self = (JsonObject*)type->tp_alloc(type, 0);
    self->obj = NULL;
    self->own = FALSE;
    self->valid = TRUE;

    /* JsonObject is defined as a c subclass of the PyObject.
     * i should be able to cast here safely because i'm casting
     * a pointer either way */
    return (PyObject *)self;
}

int
JsonObject_init(JsonObject *self, PyObject *args, PyObject *kwds)
{
    self->obj = BongoJsonObjectNew();
    self->own = TRUE;
    self->valid = TRUE;
    return 0;
}

PyObject *
JsonObject_str(PyObject *selfp)
{
    JsonObject *self = (JsonObject *)selfp;
    PyObject *ret;
    char *str;
    
    str = BongoJsonObjectToString(self->obj);

    ret = Py_BuildValue("s", str);

    MemFree(str);

    return ret;
}

Py_ssize_t
JsonObject_length(PyObject *self)
{
    /* FIXME */
    return 1;
}

PyObject *
JsonObject_subscript(PyObject *selfp, PyObject *key)
{
    JsonObject *self = (JsonObject*)selfp;
    char *str;
    BongoJsonNode *node;
    BongoJsonResult res;
    
    if (!PyString_Check(key)) {
        PyErr_SetString(PyExc_KeyError,  "Key must be a string");
        return NULL;
    }

    str = PyString_AsString(key);

    res = BongoJsonObjectGet(self->obj, str, &node);
    
    if (res != BONGO_JSON_OK) {
        RaiseJsonError(res);
        return NULL;
    }

    return JsonNodeToPy(node, FALSE, FALSE);
}

int
JsonObject_subscript_assign(PyObject *selfp, PyObject *key, PyObject *value)
{
    JsonObject *self = (JsonObject*)selfp;
    BongoJsonNode *node;
    BongoJsonResult res;
    
    node = JsonPyToNode(value);
    if (!node) {
        return 0;
    }

    if (!PyString_Check(key)) {
        PyErr_SetString(PyExc_KeyError,  "Key must be a string");
        return 0;
    }
    
    res = BongoJsonObjectPut(self->obj, PyString_AsString(key), node);
    if (res != BONGO_JSON_OK) {
        RaiseJsonError(res);
        return 0;
    }

    return 0;
}

/* Static */

BOOL
BongoJsonPreInit(void)
{
    JsonObjectType.tp_new = PyType_GenericNew;
    if (PyType_Ready(&JsonObjectType) < 0) {
        return FALSE;
    }

    JsonArrayType.tp_new = PyType_GenericNew;
    if (PyType_Ready(&JsonArrayType) < 0) {
        return FALSE;
    }

    return TRUE;
}

BOOL
BongoJsonPostInit(PyObject *module)
{
    Py_INCREF(&JsonObjectType);
    PyModule_AddObject(module, "JsonObject", (PyObject *)&JsonObjectType);
    Py_INCREF(&JsonArrayType);
    PyModule_AddObject(module, "JsonArray", (PyObject *)&JsonArrayType);
    return TRUE;
}


static PyObject *
bongojson_BongoJsonParseString(PyObject *self, PyObject *args)
{
    char *jsonStr;
    BongoJsonNode *node;
    BongoJsonResult res;
    PyObject *ret;
    PyObject *obj;
    PyObject *strObj = NULL;

    if (!PyArg_ParseTuple(args, "O", &obj)) {
        PyErr_SetString(PyExc_TypeError, "ParseString takes one argument");
        return NULL;
    }
    
    if (PyString_Check(obj)) {
        jsonStr = PyString_AsString(obj);
    } else if (PyUnicode_Check(obj)) {
        strObj = PyUnicode_AsUTF8String(obj);
        jsonStr = PyString_AsString(strObj);
    } else {
        PyErr_SetString(PyExc_TypeError, "Argument 1 must be a string or unicode object");
        return NULL;
    }
    
    res = BongoJsonParseString(jsonStr, &node);
    
    if (strObj) {
        Py_DECREF(strObj);
    }

    if (res != BONGO_JSON_OK) {
        RaiseJsonError(res);
        return NULL;
    }

    /* Pull out the value and throw away the node */
    ret = JsonNodeToPy(node, FALSE, FALSE);
    if (node->type == BONGO_JSON_STRING) {
        BongoJsonNodeFree(node);
    } else {
        BongoJsonNodeFreeSteal(node);
    }
    
    return ret;
}

static PyObject *
bongojson_ToPy(PyObject *self, PyObject *args)
{
    PyObject *obj;
    PyObject *ret;

    if (!PyArg_ParseTuple(args, "O", &obj)) {
        PyErr_SetString(PyExc_TypeError, "ToPy takes one argument");
        return NULL;
    }

    if (PyObject_IsInstance(obj, (PyObject*)&JsonObjectType)) {
        JsonObject *json = ((JsonObject*)obj);
        ret = JsonObjectToPyNative(json->obj);
        if (!ret) {
            PyErr_SetString(PyExc_RuntimeError, "Couldn't convert json to python");
            return NULL;
        }
        return ret;
    } else if (PyObject_IsInstance(obj, (PyObject*)&JsonArrayType)) {
        JsonArray *json = ((JsonArray*)obj);
        ret = JsonArrayToPyNative(json->array);
        if (!ret) {
            PyErr_SetString(PyExc_RuntimeError, "Couldn't convert json to python");
            return NULL;
        }
        return ret;
    } else {
        PyErr_SetString(PyExc_TypeError, "Argument 1 must be a bongojson object or array");
        return NULL;
    }
}

PyMethodDef BongoJsonMethods[] = {
    {"ParseString", bongojson_BongoJsonParseString, METH_VARARGS | METH_STATIC, "Parse a json string"},
    {"ToPy", bongojson_ToPy, METH_VARARGS | METH_STATIC, "Convert a json object to a native python object"},
    {NULL, NULL, 0, NULL}
};

XPL_END_C_LINKAGE
