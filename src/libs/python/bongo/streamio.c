#include <Python.h>

#include <bongo-config.h>
#include <streamio.h>
#include <bongostream.h>

#ifdef __cplusplus
extern "C" {
#endif

PyDoc_STRVAR(BongoStreamAddCodec_doc,
"BongoStreamAddCodec(stream, codec, encode)\n\
\n\
Add a codec to a stream.");

static PyObject *
streamio_BongoStreamAddCodec(PyObject *self, PyObject *args)
{
    PyObject *pystream = NULL;
    BongoStream *stream = NULL;
    char *codec = NULL;
    PyObject *pyencode = NULL;
    int ret;
    PyObject *pyret;
    
    if (!PyArg_ParseTuple(args, "OsO", &pystream, &codec, &pyencode)) {
        return NULL;
    }
    stream = PyCObject_AsVoidPtr(pystream);

    if (!PyBool_Check(pyencode)) {
        PyErr_SetString(PyExc_TypeError,
                        "BongoStreamCreate: arg 2 must be bool");
        return NULL;
    }

    ret = BongoStreamAddCodec(stream, codec, (pyencode == Py_True));

    /* returns zero on success */
    pyret = (!ret ? Py_True : Py_False);

    Py_INCREF(pyret);
    return pyret;
}

PyDoc_STRVAR(BongoStreamCreate_doc,
"BongoStreamCreate(codecs, encode) -> stream\n\
\n\
Return a pointer to a BongoStream.");

static PyObject *
streamio_BongoStreamCreate(PyObject *self, PyObject *args)
{
    PyObject *pycodecs = NULL;
    PyObject *pyencode = NULL;
    BongoStream *stream = NULL;
    char **codecs = NULL;
    int codecsLen = 0;
    int i;

    if (!PyArg_ParseTuple(args, "OO", &pycodecs, &pyencode)) {
        return NULL;
    }
    if (!PyList_Check(pycodecs)) {
        PyErr_SetString(PyExc_TypeError,
                        "BongoStreamCreate: arg 1 must be list");
        return NULL;
    }
    if (!PyBool_Check(pyencode)) {
        PyErr_SetString(PyExc_TypeError,
                        "BongoStreamCreate: arg 2 must be bool");
        return NULL;
    }

    codecsLen = PyList_Size(pycodecs);
    codecs = MemMalloc(sizeof(char *) * codecsLen);
    for (i=0; i<codecsLen; i++) {
        codecs[i] = PyString_AsString(PyList_GetItem(pycodecs, i));
        if (codecs[i] == NULL) {
            MemFree(codecs);
            return NULL;
        }
    }

    stream = BongoStreamCreate(codecs, codecsLen, (pyencode == Py_True));
    MemFree(codecs);

    if (stream == NULL) {
        Py_INCREF(Py_None);
        return Py_None;
    }
    
    return PyCObject_FromVoidPtr(stream, (void *)BongoStreamFree);
}

PyDoc_STRVAR(BongoStreamEos_doc,
"BongoStreamEos(stream)\n\
\n\
Close down a stream.");

static PyObject *
streamio_BongoStreamEos(PyObject *self, PyObject *args)
{
    PyObject *pystream = NULL;
    BongoStream *stream = NULL;

    if (!PyArg_ParseTuple(args, "O", &pystream)) {
        return NULL;
    }
    stream = PyCObject_AsVoidPtr(pystream);

    BongoStreamEos(stream);

    Py_INCREF(Py_None);
    return Py_None;
}

PyDoc_STRVAR(BongoStreamGet_doc,
"BongoStreamGet(stream, buffer) -> count\n\
\n\
Read as many bytes as possible from the stream, and put them in buffer.\
Return the number of bytes read.");

static PyObject *
streamio_BongoStreamGet(PyObject *self, PyObject *args)
{
    PyObject *pystream = NULL;
    BongoStream *stream = NULL;
    PyObject *pybuf = NULL;

    char *buf;
    int bufLen;
    int read = 0;

    if (!PyArg_ParseTuple(args, "OO", &pystream, &pybuf)) {
        return NULL;
    }
    stream = PyCObject_AsVoidPtr(pystream);

    if (PyObject_AsCharBuffer(pybuf, (const char**)&buf, &bufLen)) {
        PyErr_SetString(PyExc_TypeError, "BongoStreamGet() buffer must be a string");
        return NULL;
    }

    read = BongoStreamGet(stream, buf, bufLen);

    return Py_BuildValue("i", read);
}

PyDoc_STRVAR(BongoStreamPut_doc,
"BongoStreamPut(stream, buffer)\n\
\n\
Put as many bytes possible from buffer into the stream.");

static PyObject *
streamio_BongoStreamPut(PyObject *self, PyObject *args)
{
    PyObject *pystream = NULL;
    BongoStream *stream = NULL;
    PyObject *pybuf = NULL;

    const char *buf;
    int bufLen;

    if (!PyArg_ParseTuple(args, "OO", &pystream, &pybuf)) {
        return NULL;
    }
    stream = PyCObject_AsVoidPtr(pystream);

    if (PyObject_AsCharBuffer(pybuf, &buf, &bufLen)) {
        PyErr_SetString(PyExc_TypeError, "BongoStreamPut() buffer must be a string");
        return NULL;
    }

    BongoStreamPut(stream, buf, bufLen);

    Py_INCREF(Py_None);
    return Py_None;
}

PyMethodDef StreamIOMethods[] = {
    {"BongoStreamAddCodec", streamio_BongoStreamAddCodec, METH_VARARGS | METH_STATIC, BongoStreamAddCodec_doc},
    {"BongoStreamCreate", streamio_BongoStreamCreate, METH_VARARGS | METH_STATIC, BongoStreamCreate_doc},
    {"BongoStreamEos", streamio_BongoStreamEos, METH_VARARGS | METH_STATIC, BongoStreamEos_doc},
    {"BongoStreamGet", streamio_BongoStreamGet, METH_VARARGS | METH_STATIC, BongoStreamGet_doc},
    {"BongoStreamPut", streamio_BongoStreamPut, METH_VARARGS | METH_STATIC, BongoStreamPut_doc},
    {NULL, NULL, 0, NULL}
};

#ifdef __cplusplus
}
#endif
