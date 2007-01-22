#include <Python.h>

#include <bongo-config.h>
#include <calcmd.h>
#include <msgapi.h>

#include "libs.h"
#include <connio.h>

#ifdef __cplusplus
extern "C" {
#endif

static PyObject *
calcmd_CalCmdNew(PyObject *self, PyObject *args)
{
    char *cmdstr;
    char *tzid;
    CalCmdCommand *cmd;

    if (!PyArg_ParseTuple(args, "sz", &cmdstr, &tzid)) {
        return NULL;
    }

    cmd = CalCmdNew(cmdstr, BongoCalTimezoneGetSystem(tzid));

    if (cmd == NULL) {
        PyErr_SetString(PyExc_ValueError, "could not parse calendar command");
        return NULL;
    }

    return PyCObject_FromVoidPtr(cmd, (void *)CalCmdFree);
}

static PyObject *
PyTuple_FromCalTime(BongoCalTime time)
{
    return Py_BuildValue("(iiiiii)", 
                         time.second, time.minute,
                         time.hour, time.day,
                         time.month + 1, time.year);
}

static PyObject *
calcmd_CalCmdGetBegin(PyObject *self, PyObject *args)
{
    CalCmdCommand *cmd;
    PyObject *cmdObj;

    if (!PyArg_ParseTuple(args, "O", &cmdObj)) {
        return NULL;
    }

    if (cmdObj == Py_None) {
        PyErr_SetString(PyExc_ValueError, "calcmd object is None");
        return NULL;
    }

    cmd = PyCObject_AsVoidPtr(cmdObj);
    return PyTuple_FromCalTime(cmd->begin);
}

static PyObject *
calcmd_CalCmdGetData(PyObject *self, PyObject *args)
{
    CalCmdCommand *cmd;
    PyObject *cmdObj;

    if (!PyArg_ParseTuple(args, "O", &cmdObj)) {
        return NULL;
    }

    if (cmdObj == Py_None) {
        PyErr_SetString(PyExc_ValueError, "calcmd object is None");
        return NULL;
    }

    cmd = PyCObject_AsVoidPtr(cmdObj);
    return Py_BuildValue("s", cmd->data);
}

static PyObject *
calcmd_CalCmdGetEnd(PyObject *self, PyObject *args)
{
    CalCmdCommand *cmd;
    PyObject *cmdObj;

    if (!PyArg_ParseTuple(args, "O", &cmdObj)) {
        return NULL;
    }

    if (cmdObj == Py_None) {
        PyErr_SetString(PyExc_ValueError, "calcmd object is None");
        return NULL;
    }

    cmd = PyCObject_AsVoidPtr(cmdObj);
    return PyTuple_FromCalTime(cmd->end);
}

static PyObject *
calcmd_CalCmdGetType(PyObject *self, PyObject *args)
{
    CalCmdCommand *cmd;
    PyObject *cmdObj;
    int type;

    if (!PyArg_ParseTuple(args, "O", &cmdObj)) {
        return NULL;
    }

    if (cmdObj == Py_None) {
        PyErr_SetString(PyExc_ValueError, "calcmd object is None");
        return NULL;
    }

    cmd = PyCObject_AsVoidPtr(cmdObj);
    type = CalCmdGetType(cmd);

    return Py_BuildValue("i", type);
}

static PyObject *
calcmd_CalCmdGetJson(PyObject *self, PyObject *args)
{
    CalCmdCommand *cmd;
    PyObject *cmdObj;
    PyObject *ret;
    BongoCalObject *cal;
    char uid[CONN_BUFSIZE];

    if (!PyArg_ParseTuple(args, "O", &cmdObj)) {
        return NULL;
    }

    if (cmdObj == Py_None) {
        PyErr_SetString(PyExc_ValueError, "calcmd object is None");
        return NULL;
    }

    cmd = PyCObject_AsVoidPtr(cmdObj);

    MsgGetUid(uid, CONN_BUFSIZE);

    cal = CalCmdProcessNewAppt(cmd, uid);
    if (!cal) {
        PyErr_SetString(PyExc_ValueError, "couldn't create calendar object");
        return NULL;
    }
    
    printf("cal is %s\n", BongoJsonObjectToString(BongoCalObjectGetJson(cal)));

    ret = JsonObjectToPy(BongoCalObjectGetJson(cal), TRUE);

    BongoCalObjectFree(cal, FALSE);

    return ret;
}


static PyObject *
calcmd_CalCmdGetConciseDateTimeRange(PyObject *self, PyObject *args)
{
    CalCmdCommand *cmd;
    PyObject *cmdObj;
    char buf[128];

    if (!PyArg_ParseTuple(args, "O", &cmdObj)) {
        return NULL;
    }

    if (cmdObj == Py_None) {
        PyErr_SetString(PyExc_ValueError, "calcmd object is None");
        return NULL;
    }

    cmd = PyCObject_AsVoidPtr(cmdObj);

    CalCmdPrintConciseDateTimeRange(cmd, buf, sizeof(buf));
    return Py_BuildValue("s", buf);
}


PyMethodDef CalCmdMethods[] = {
    {"New", calcmd_CalCmdNew, METH_VARARGS | METH_STATIC},
    {"GetBegin", calcmd_CalCmdGetBegin, METH_VARARGS | METH_STATIC},
    {"GetData", calcmd_CalCmdGetData, METH_VARARGS | METH_STATIC},
    {"GetEnd", calcmd_CalCmdGetEnd, METH_VARARGS | METH_STATIC},
    {"GetType", calcmd_CalCmdGetType, METH_VARARGS | METH_STATIC},
    {"GetJson", calcmd_CalCmdGetJson, METH_VARARGS | METH_STATIC},
    {"GetConciseDateTimeRange", 
     calcmd_CalCmdGetConciseDateTimeRange, METH_VARARGS | METH_STATIC },
    {NULL, NULL, 0, NULL}
};

EnumItemDef CalCmdEnums[] = {
    {"ERROR", CALCMD_ERROR, NULL},
    {"QUERY", CALCMD_QUERY, NULL},
    {"QUERY_CONFLICTS", CALCMD_QUERY_CONFLICTS, NULL},
    {"NEW_APPT", CALCMD_NEW_APPT, NULL},
    {"HELP", CALCMD_HELP, NULL},
    {NULL, 0, NULL}
};

#ifdef __cplusplus
}
#endif
