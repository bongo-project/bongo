#ifndef PYBONGO_JSON_H
#define PYBONGO_JSON_H

#include <Python.h>
#include <structmember.h>

#include <bongojson.h>
#include <bongocal.h>

#ifdef __cplusplus
extern "C" {
#endif

BOOL BongoJsonPreInit(void);
BOOL BongoJsonPostInit(PyObject *module);

/*** JsonObject ***/

typedef struct {
    PyObject_HEAD
    BongoJsonObject *obj;
    BOOL own;
    BOOL valid;
} JsonObject;

/*** JsonArray ***/

typedef struct {
    PyObject_HEAD
    BOOL own;
    GArray *array;
} JsonArray;
    
/* Object protocol */
PyObject *JsonObject_new(PyTypeObject *type, PyObject *args, PyObject *kwds);
int JsonObject_init(JsonObject *self, PyObject *args, PyObject *kwds);
void JsonObject_dealloc(JsonObject *self);
PyObject *JsonObject_str(PyObject *selfp);

/* Mapping protocol */
int JsonObject_length(PyObject *self);
PyObject *JsonObject_subscript(PyObject *self, PyObject *key);
int JsonObject_subscript_assign(PyObject *self, PyObject *key, PyObject *value);

extern PyTypeObject JsonObjectType;


/* Object protocol */
PyObject *JsonArray_new(PyTypeObject *type, PyObject *args, PyObject *kwds);
int JsonArray_init(JsonArray *self, PyObject *args, PyObject *kwds);
void JsonArray_dealloc(JsonArray *self);
PyObject *JsonArray_str(PyObject *selfp);

/* Methods */

PyObject *JsonArray_append(PyObject *selfp, PyObject *args);

extern PyTypeObject JsonArrayType;

/* Sequence protocol */

int JsonArray_length(PyObject *selfp);
PyObject *JsonArray_concat(PyObject *selfp, PyObject *b);
PyObject *JsonArray_repeat(PyObject *selfp, int i);
PyObject *JsonArray_item(PyObject *selfp, int i);
PyObject *JsonArray_slice(PyObject *selfp, int i1, int i2);
int JsonArray_assign(PyObject *selfp, int i, PyObject *obj);
int JsonArray_assign_slice(PyObject *selfp, int i1, int i2, PyObject *obj);
int JsonArray_contains(PyObject *selfp, PyObject *b);
PyObject *JsonArray_inplace_concat(PyObject *selfp, PyObject *b);
PyObject *JsonArray_inplace_repeat(PyObject *selfp, int i);


PyObject *JsonObjectToPy(BongoJsonObject *object, BOOL own);
PyObject *JsonNodeToPy(BongoJsonNode *node, BOOL own, BOOL native);

#ifdef __cplusplus
}
#endif

#endif /* PYBONGO_JSON_H */
