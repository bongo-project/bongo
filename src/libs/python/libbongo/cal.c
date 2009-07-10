#include <Python.h>
#include <datetime.h>

#include <bongojson.h>
#include <bongocal.h>
#include <bongocal-raw.h>

#include "libs.h"

#ifdef __cplusplus
extern "C" {
#endif

static PyObject *
cal_Merge(PyObject *self, PyObject *args)
{
    JsonObject *a;
    JsonObject *b;
    JsonObject *ret;
    
    if (!PyArg_ParseTuple(args, "OO", &a, &b)) {
        PyErr_SetString(PyExc_TypeError, "Merge() takes 2 arguments");
        return NULL;
    }
    
    BongoCalMerge(a->obj, b->obj);

    /* caller shouldn't use the old objects anymore */
    a->own = FALSE;
    a->valid = FALSE;
    b->own = FALSE;
    b->valid = FALSE;

    ret = PyObject_New(JsonObject, &JsonObjectType);
    ret->obj = a->obj;
    ret->own = TRUE;
    ret->valid = TRUE;

    return (PyObject*)ret;
}

static int
GetIntAttr(PyObject *obj, char *attrname, int defval)
{
    PyObject *attr;
    int ret;
    
    attr = PyObject_GetAttrString(obj, attrname);
    if (PyInt_Check(attr)) {
        ret = PyInt_AsLong(attr);
    } else if (PyLong_Check(attr)) {
        ret = PyLong_AsLong(attr);
    } else {
        ret = defval;
    }
    
    Py_DECREF(attr);

    return ret;
}

static BongoCalTime
CalTimeFromPy(PyObject *dt)
{
    BongoCalTime t;
    
    t = BongoCalTimeEmpty();
    
    t.year = GetIntAttr(dt, "year", 1970);
    t.month = GetIntAttr(dt, "month", 1) - 1;
    t.day = GetIntAttr(dt, "day", 1);
    t.hour = GetIntAttr(dt, "hour", 0);
    t.minute = GetIntAttr(dt, "minute", 0);
    t.second = GetIntAttr(dt, "second", 0);

    return t;
}

static PyObject *
cal_PrimaryOccurrence(PyObject *self, PyObject *args)
{
    JsonObject *json;
    const char *tzid;
    BongoCalObject *cal;
    BongoCalOccurrence occ;
    BongoCalTimezone *tz;

    BongoJsonObject *occJson;

    if (!PyArg_ParseTuple(args, "Oz", &json, &tzid)) {
        PyErr_SetString(PyExc_TypeError, "PrimaryOccurrence() takes 2 arguments");
        return NULL;
    }

    cal = BongoCalObjectNew(json->obj);
    tz = BongoCalObjectGetTimezone(cal, tzid);

    occ = BongoCalObjectPrimaryOccurrence(cal, tz);

    if (BongoCalTimeIsEmpty(occ.start)) {
        Py_INCREF(Py_None);
        return Py_None;
    }

    occJson = BongoCalOccurrenceToJson(occ, tz);
    return JsonObjectToPy(occJson, TRUE);
}

static PyObject *
cal_Collect(PyObject *self, PyObject *args)
{
    JsonObject *json;
    BongoCalObject *cal;
    BongoArray *occs;
    PyObject *dtstart;
    PyObject *dtend;
    const char *tzid;
    BongoCalTime start;
    BongoCalTime end;
    
    if (!PyArg_ParseTuple(args, "OOOs", &json, &dtstart, &dtend, &tzid)) {
        PyErr_SetString(PyExc_TypeError, "Collect() takes 4 arguments");
        return NULL;
    }

    if (!dtstart || !dtend) {
        PyErr_SetString(PyExc_TypeError, "Collect() requires a start and end time");
        return NULL;
    }

#ifdef PyDateTime_IMPORT
    if (!PyDate_Check(dtstart) || !PyDate_Check(dtend)) {
        PyErr_SetString(PyExc_TypeError, "Invalid type for dtstart or dtend (needs DateTime)");
        return NULL;
    }
#endif

    start = CalTimeFromPy(dtstart);
    end = CalTimeFromPy(dtend);
    
    cal = BongoCalObjectNew(json->obj);
    
    occs = BongoArrayNew(sizeof(BongoCalOccurrence), 16);
    if (BongoCalObjectCollect(cal, start, end, NULL, TRUE, occs)) {
        JsonArray *ret;
        BongoArray *occsJson;
        occsJson = BongoCalOccurrencesToJson(occs, BongoCalObjectGetTimezone(cal, tzid));
        BongoArrayFree(occs, TRUE);

        ret = PyObject_New(JsonArray, &JsonArrayType);
        ret->array = occsJson;
        ret->own = TRUE;

        return (PyObject*)ret;
    } else {
        BongoArrayFree(occs, TRUE);
        Py_INCREF(Py_None);
        return Py_None;
    }
}

static PyObject *
cal_JsonToIcal(PyObject *self, PyObject *args)
{
    JsonObject *a;
    BongoCalObject *cal;
    icalcomponent *comp;
    
    if (!PyArg_ParseTuple(args, "O", &a)) {
        PyErr_SetString(PyExc_TypeError, "JsonToIcal() takes 2 arguments");
        return NULL;
    }
    
    cal = BongoCalObjectNew(a->obj);
    BongoCalObjectResolveSystemTimezones(cal);
    BongoCalObjectFree(cal, FALSE);

    comp = BongoCalJsonToIcal(a->obj);
    
    if (comp) {
        PyObject *ret;
        char *data = icalcomponent_as_ical_string(comp);
        ret = PyUnicode_Decode(data, strlen(data), "utf-8", "strict");
        icalcomponent_free(comp);
        return ret;
    } else {
        PyErr_SetString(PyExc_ValueError, "Couldn't convert json object");
        return NULL;
    }
}

static PyObject *
cal_IcalToJson(PyObject *self, PyObject *args)
{
    BongoJsonObject *json;
    icalcomponent *comp;
    char *str;    
    
    if (!PyArg_ParseTuple(args, "s", &str)) {
        PyErr_SetString(PyExc_TypeError, "IcalToJson() takes 2 arguments");
        return NULL;
    }
    
    comp = icalparser_parse_string(str);
    json = BongoCalIcalToJson(comp);
    icalcomponent_free(comp);

    return JsonObjectToPy(json, TRUE);
}

static PyObject *
cal_BongoCalTimeToStringConcise(PyObject *self, PyObject *args)
{
    BongoCalTime t;
    char *str;
    char buf[128];

    if (!PyArg_ParseTuple(args, "s", &str)) {
        PyErr_SetString(PyExc_TypeError, "BongoCalTimeToStringConcise() takes 1 arguments");
        return NULL;
    }

    t = BongoCalTimeParseIcal(str);
    if (BongoCalTimeIsEmpty(t)) {
        PyErr_SetString(PyExc_ValueError, "could not parse ical time");
        return NULL;
    }

    BongoCalTimeToStringConcise(t, buf, sizeof(buf));

    return Py_BuildValue("s", buf);

}

static BongoCalTimezone *
GetTz(const char *tzid, BongoCalObject **cal, const char *json)
{
    if (!strcmp(tzid, "/bongo/UTC")) {
        return BongoCalTimezoneGetUtc();
    }

    if (!strncmp(tzid, "/bongo/", 6)) {
        return BongoCalTimezoneGetSystem(tzid);
    }

    if (*cal == NULL) {
        *cal = BongoCalObjectParseString(json);
    }

    if (*cal == NULL) {
        return NULL;
    }
    
    return BongoCalObjectGetTimezone(*cal, tzid);
}

static PyObject *
cal_ConvertTzid(PyObject *self, PyObject *args)
{
    BongoCalTime t;
    const char *json;
    const char *timestr;
    const char *tzidFrom;
    const char *tzidTo;
    BongoCalObject *cal = NULL;
    char buf[BONGO_CAL_TIME_BUFSIZE + 1];
    BongoCalTimezone *tzFrom;
    BongoCalTimezone *tzTo;

    if (!PyArg_ParseTuple(args, "ssss", &json, &timestr, &tzidFrom, &tzidTo)) {
        PyErr_SetString(PyExc_TypeError, "ConvertTzid() takes 4 arguments");
        return NULL;
    }

    tzFrom = GetTz(tzidFrom, &cal, json);
    tzTo = GetTz(tzidTo, &cal, json);

    if (!tzFrom) {
        PyErr_SetString(PyExc_RuntimeError, "Couldn't find tzid");
        return NULL;
    } 

    if (!tzTo) {
        PyErr_SetString(PyExc_RuntimeError, "Couldn't find tzid");
        return NULL;
    }

    t = BongoCalTimeParseIcal(timestr);
    if (BongoCalTimeIsEmpty(t)) {
        PyErr_SetString(PyExc_RuntimeError, "Couldn't parse time");
        return NULL;
    }
    t = BongoCalTimeSetTimezone(t, tzFrom);
    t = BongoCalTimezoneConvertTime(t, tzTo);
    
    BongoCalTimeToIcal(t, buf, BONGO_CAL_TIME_BUFSIZE);

    if (cal) {
        BongoCalObjectFree(cal, TRUE);
    }
    
    return Py_BuildValue("s", buf);
}

int
BongoCalPostInit(PyObject *module)
{
#ifdef PyDateTime_IMPORT
    PyDateTime_IMPORT;
#endif

    return 0;
}

PyMethodDef CalMethods[] = {
    {"Merge", cal_Merge, METH_VARARGS | METH_STATIC},
    {"Collect", cal_Collect, METH_VARARGS | METH_STATIC},
    {"IcalToJson", cal_IcalToJson, METH_VARARGS | METH_STATIC},
    {"JsonToIcal", cal_JsonToIcal, METH_VARARGS | METH_STATIC},
    {"PrimaryOccurrence", cal_PrimaryOccurrence, METH_VARARGS | METH_STATIC},
    {"TimeToStringConcise", cal_BongoCalTimeToStringConcise, METH_VARARGS | METH_STATIC },
    {"ConvertTzid", cal_ConvertTzid, METH_VARARGS | METH_STATIC },
    {NULL, NULL, 0, NULL}
};

#ifdef __cplusplus
}
#endif
