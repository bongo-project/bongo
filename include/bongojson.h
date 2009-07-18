/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

#ifndef BONGOJSON_H
#define BONGOJSON_H

#include <xpl.h>
#include <bongoutil.h>
#include <glib.h>

XPL_BEGIN_C_LINKAGE

typedef struct _BongoJsonNode       BongoJsonNode;
typedef struct _BongoJsonObject     BongoJsonObject;
typedef struct _BongoJsonObjectIter BongoJsonObjectIter;

typedef struct _BongoJsonParser     BongoJsonParser;

typedef enum {
    BONGO_JSON_NULL,
    BONGO_JSON_OBJECT,
    BONGO_JSON_ARRAY,
    BONGO_JSON_BOOL,
    BONGO_JSON_DOUBLE,
    BONGO_JSON_INT,
    BONGO_JSON_STRING
} BongoJsonType;

typedef enum {
    BONGO_JSON_OK = 0,
    BONGO_JSON_NOT_FOUND = -1,
    BONGO_JSON_BAD_TYPE = -2,
    BONGO_JSON_UNEXPECTED_CHAR = -3,
    BONGO_JSON_BOUNDS_ERROR = -4,
    BONGO_JSON_UNCLOSED_COMMENT = -5,
    BONGO_JSON_UNCLOSED_STRING = -6,
    BONGO_JSON_UNCLOSED_OBJECT = -6,
    BONGO_JSON_UNCLOSED_ARRAY = -7,
    BONGO_JSON_MISSING_VALUE = -8,
    BONGO_JSON_BAD_KEY = -9,
    BONGO_JSON_UNEXPECTED_EOF = -10,
    BONGO_JSON_UNKNOWN_ERROR = -11,
} BongoJsonResult;

struct _BongoJsonNode {
    BongoJsonType type;
    
    union {
        BongoJsonObject *objectVal;
        GArray *arrayVal;
        BOOL boolVal;
        double doubleVal;
        int intVal;
        char *stringVal;
    } value;
};

typedef int (*BongoJsonParserReadFunc)(void *parserData, char *buffer, int bufLen);
typedef void (*BongoJsonParserFreeFunc)(void *parserData);

/*** BongoJsonNode ***/

BongoJsonNode *BongoJsonNodeNewNull(void);
BongoJsonNode *BongoJsonNodeNewObject(BongoJsonObject *val);
BongoJsonNode *BongoJsonNodeNewArray(GArray *val);
BongoJsonNode *BongoJsonNodeNewBool(BOOL val);
BongoJsonNode *BongoJsonNodeNewDouble(double val);
BongoJsonNode *BongoJsonNodeNewInt(int val);
BongoJsonNode *BongoJsonNodeNewString(const char *val);
BongoJsonNode *BongoJsonNodeNewStringGive(char *val);

BongoJsonNode *BongoJsonNodeDup(BongoJsonNode *node);

#define BongoJsonNodeAsObject(n) ((n)->value.objectVal)
#define BongoJsonNodeAsArray(n) ((n)->value.arrayVal)
#define BongoJsonNodeAsBool(n) ((n)->value.boolVal)
#define BongoJsonNodeAsDouble(n) ((n)->value.doubleVal)
#define BongoJsonNodeAsInt(n) ((n)->value.intVal)
#define BongoJsonNodeAsString(n) ((n)->value.stringVal)

char *BongoJsonNodeToString(BongoJsonNode *node);
void BongoJsonNodeToStringBuilder(BongoJsonNode *node, BongoStringBuilder *sb);

void BongoJsonNodeFree(BongoJsonNode *node);
void BongoJsonNodeFreeSteal(BongoJsonNode *node);

/*** BongoJsonArray **/

#define BongoJsonArrayNew(count) BongoArrayNew(sizeof(BongoJsonNode*), count);

void BongoJsonArrayAppend(GArray *array, BongoJsonNode *node);

#define BongoJsonArrayAppendObject(a, v) BongoJsonArrayAppend((a), BongoJsonNodeNewObject(v))
#define BongoJsonArrayAppendArray(a, v) BongoJsonArrayAppend((a), BongoJsonNodeNewArray(v))
#define BongoJsonArrayAppendBool(a, v) BongoJsonArrayAppend((a), BongoJsonNodeNewBool(v))
#define BongoJsonArrayAppendDouble(a, v) BongoJsonArrayAppend((a), BongoJsonNodeNewDouble(v))
#define BongoJsonArrayAppendInt(a, v) BongoJsonArrayAppend((a), BongoJsonNodeNewInt(v))
#define BongoJsonArrayAppendString(a, v) BongoJsonArrayAppend((a), BongoJsonNodeNewString(v))
#define BongoJsonArrayAppendStringGive(a, v) BongoJsonArrayAppend((a), BongoJsonNodeNewStringGive(v))

void BongoJsonArrayToStringBuilder(GArray *array, BongoStringBuilder *sb);
char *BongoJsonArrayToString(GArray *array);

void BongoJsonArrayRemove(GArray *array, int i);
void BongoJsonArrayRemoveSteal(GArray *array, int i);

#define BongoJsonArrayGet(a, i) g_array_index((a), BongoJsonNode*, (i))

#define DEF_ARRAY_GET(ctype, etype, name) \
    static __inline BongoJsonResult BongoJsonArrayGet##name(GArray *array, int i, ctype *val) \
    {                                                                   \
        BongoJsonNode *node = BongoJsonArrayGet(array, i);                \
        if (node->type == etype) {                                      \
            *val = BongoJsonNodeAs##name(node);                          \
            return BONGO_JSON_OK;                                        \
        } else {                                                        \
            return BONGO_JSON_BAD_TYPE;                                  \
        }                                                               \
    }

DEF_ARRAY_GET(BongoJsonObject *, BONGO_JSON_OBJECT, Object);
DEF_ARRAY_GET(GArray *, BONGO_JSON_ARRAY, Array);
DEF_ARRAY_GET(BOOL, BONGO_JSON_BOOL, Bool);
DEF_ARRAY_GET(double, BONGO_JSON_DOUBLE, Double);
DEF_ARRAY_GET(int, BONGO_JSON_INT, Int);
DEF_ARRAY_GET(const char *, BONGO_JSON_STRING, String);

#undef DEF_ARRAY_GET

GArray *BongoJsonArrayDup(GArray *array);

void BongoJsonArrayFree(GArray *array);

/*** BongoJsonObject ***/

struct _BongoJsonObjectIter {
    /* public */
    const char *key;
    BongoJsonNode *value;
    
    /* private */
    BongoHashtableIter iter;
};

BongoJsonObject *BongoJsonObjectNew(void);
void BongoJsonObjectFree(BongoJsonObject *obj);
BongoJsonObject *BongoJsonObjectDup(BongoJsonObject *obj);

void BongoJsonObjectToStringBuilder(BongoJsonObject *obj, BongoStringBuilder *sb);
char *BongoJsonObjectToString(BongoJsonObject *obj);

BOOL BongoJsonObjectIterFirst(BongoJsonObject *obj, BongoJsonObjectIter *iter);
BOOL BongoJsonObjectIterNext(BongoJsonObject *obj, BongoJsonObjectIter *iter);

BongoJsonResult BongoJsonObjectGet(BongoJsonObject *obj, const char *key, BongoJsonNode **node);

BongoJsonResult BongoJsonObjectGetObject(BongoJsonObject *obj, const char *key, BongoJsonObject **val);
BongoJsonResult BongoJsonObjectGetArray(BongoJsonObject *obj, const char *key, GArray **val);
BongoJsonResult BongoJsonObjectGetBool(BongoJsonObject *obj, const char *key, BOOL *val);
BongoJsonResult BongoJsonObjectGetDouble(BongoJsonObject *obj, const char *key, double *val);
BongoJsonResult BongoJsonObjectGetInt(BongoJsonObject *obj, const char *key, int *val);
BongoJsonResult BongoJsonObjectGetString(BongoJsonObject *obj, const char *key, const char **val);

BongoJsonResult BongoJsonObjectPut(BongoJsonObject *obj, const char *key, BongoJsonNode *node);
BongoJsonResult BongoJsonObjectPutNull(BongoJsonObject *obj, const char *key);
BongoJsonResult BongoJsonObjectPutObject(BongoJsonObject *obj, const char *key, BongoJsonObject *val);
BongoJsonResult BongoJsonObjectPutArray(BongoJsonObject *obj, const char *key, GArray *val);
BongoJsonResult BongoJsonObjectPutBool(BongoJsonObject *obj, const char *key, BOOL val);
BongoJsonResult BongoJsonObjectPutDouble(BongoJsonObject *obj, const char *key, double val);
BongoJsonResult BongoJsonObjectPutInt(BongoJsonObject *obj, const char *key, int val);
BongoJsonResult BongoJsonObjectPutString(BongoJsonObject *obj, const char *key, const char *val);
BongoJsonResult BongoJsonObjectPutStringGive(BongoJsonObject *obj, const char *key, const char *val);

BongoJsonResult BongoJsonObjectRemove(BongoJsonObject *obj, const char *key);
BongoJsonResult BongoJsonObjectRemoveSteal(BongoJsonObject *obj, const char *key);

/*** Parsing ***/

char *BongoJsonQuoteString(const char *str);
void BongoJsonQuoteStringToStringBuilder(const char *str, BongoStringBuilder *sb);

BongoJsonResult BongoJsonParseString(const char *str, BongoJsonNode **node);
BongoJsonResult BongoJsonParseFile(FILE *f, int toRead, BongoJsonNode **node);

BongoJsonResult BongoJsonValidateString(const char *str);

BongoJsonParser *BongoJsonParserNew(void *userData, 
                                  BongoJsonParserReadFunc read,
                                  BongoJsonParserFreeFunc freeFunc,
                                  int toRead);
BongoJsonParser *BongoJsonParserNewString(const char *str);
BongoJsonParser *BongoJsonParserNewFile(FILE *f, int toRead);

void BongoJsonParserFree(BongoJsonParser *parser);

BongoJsonResult BongoJsonParserReadNode(BongoJsonParser *parser, BongoJsonNode **node);

/*** JsonJPath : querying a json structure, sort of ***/

BongoJsonNode *BongoJsonJPath(BongoJsonNode *root, const char *path);

BongoJsonResult BongoJsonJPathGetObject(BongoJsonNode *n, const char *path, BongoJsonObject **val);
BongoJsonResult BongoJsonJPathGetArray(BongoJsonNode *n, const char *path, GArray **val);
BongoJsonResult BongoJsonJPathGetBool(BongoJsonNode *n, const char *path, BOOL *val);
BongoJsonResult BongoJsonJPathGetInt(BongoJsonNode *n, const char *path, int *val);
BongoJsonResult BongoJsonJPathGetString(BongoJsonNode *n, const char *path, char **val);
BongoJsonResult BongoJsonJPathGetDouble(BongoJsonNode *n, const char *path, double *val);
BongoJsonResult BongoJsonJPathGetFloat(BongoJsonNode *n, const char *path, float *val);


/*** path resolvers ***/

BongoJsonResult BongoJsonNodeResolve(BongoJsonNode *node, const char *path, BongoJsonNode **out);
BongoJsonResult BongoJsonObjectResolve(BongoJsonObject *obj, const char *path, BongoJsonNode **out);

#define DEF_TYPED_RESOLVER(ctype, etype, name)                           \
    static __inline BongoJsonResult BongoJsonNodeResolve##name(BongoJsonNode *node, const char *path, ctype *out) \
    {                                                                   \
        BongoJsonResult res = BongoJsonNodeResolve(node, path, &node);    \
        if (res != BONGO_JSON_OK) return res;                            \
        if (node->type != etype) return BONGO_JSON_BAD_TYPE;             \
        *out = BongoJsonNodeAs##name(node);                              \
        return BONGO_JSON_OK;                                            \
    } \
    static __inline BongoJsonResult BongoJsonObjectResolve##name(BongoJsonObject *obj, const char *path, ctype *out) \
    {   BongoJsonNode *node; \
        BongoJsonResult res = BongoJsonObjectResolve(obj, path, &node);    \
        if (res != BONGO_JSON_OK) return res;                            \
        if (node->type != etype) return BONGO_JSON_BAD_TYPE;             \
        *out = BongoJsonNodeAs##name(node);                              \
        return BONGO_JSON_OK;                                            \
    }   

DEF_TYPED_RESOLVER(BongoJsonObject *, BONGO_JSON_OBJECT, Object);
DEF_TYPED_RESOLVER(GArray *, BONGO_JSON_ARRAY, Array);
DEF_TYPED_RESOLVER(BOOL, BONGO_JSON_BOOL, Bool);
DEF_TYPED_RESOLVER(double, BONGO_JSON_DOUBLE, Double);
DEF_TYPED_RESOLVER(int, BONGO_JSON_INT, Int);
DEF_TYPED_RESOLVER(const char *, BONGO_JSON_STRING, String);

#undef DEF_TYPED_RESOLVER

XPL_END_C_LINKAGE

#endif
