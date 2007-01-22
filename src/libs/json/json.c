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


#include <config.h>
#include <bongojson.h>
#include <assert.h>

#define PRETTY_JSON 1

static void BongoJsonArrayToStringBuilderInternal(BongoArray *array, BongoStringBuilder *sb, int depth);
static void BongoJsonObjectToStringBuilderInternal(BongoJsonObject *object, BongoStringBuilder *sb, int depth);

BongoJsonNode *
BongoJsonNodeNewNull(void)
{
    BongoJsonNode *node = MemMalloc0(sizeof(BongoJsonNode));
    node->type = BONGO_JSON_NULL;
    return node;
}

BongoJsonNode *
BongoJsonNodeNewObject(BongoJsonObject *val)
{
    BongoJsonNode *node = MemMalloc0(sizeof(BongoJsonNode));
    node->type = BONGO_JSON_OBJECT;
    node->value.objectVal = val;
    return node;
}

BongoJsonNode *
BongoJsonNodeNewArray(BongoArray *val)
{
    BongoJsonNode *node = MemMalloc0(sizeof(BongoJsonNode));
    node->type = BONGO_JSON_ARRAY;
    node->value.arrayVal = val;
    return node;
}

BongoJsonNode *
BongoJsonNodeNewBool(BOOL val)
{
    BongoJsonNode *node = MemMalloc0(sizeof(BongoJsonNode));
    node->type = BONGO_JSON_BOOL;
    node->value.boolVal = val;
    return node;
}

BongoJsonNode *
BongoJsonNodeNewDouble(double val)
{
    BongoJsonNode *node = MemMalloc0(sizeof(BongoJsonNode));
    node->type = BONGO_JSON_DOUBLE;
    node->value.doubleVal = val;
    return node;
}

BongoJsonNode *
BongoJsonNodeNewInt(int val)
{
    BongoJsonNode *node = MemMalloc0(sizeof(BongoJsonNode));
    node->type = BONGO_JSON_INT;
    node->value.intVal = val;
    return node;
}

BongoJsonNode *
BongoJsonNodeNewString(const char *val)
{
    BongoJsonNode *node = MemMalloc0(sizeof(BongoJsonNode));
    node->type = BONGO_JSON_STRING;
    node->value.stringVal = MemStrdup(val);
    return node;
}

BongoJsonNode *
BongoJsonNodeNewStringGive(char *val)
{
    BongoJsonNode *node = MemMalloc0(sizeof(BongoJsonNode));
    node->type = BONGO_JSON_STRING;
    node->value.stringVal = val;
    return node;
}

BongoJsonNode *
BongoJsonNodeDup(BongoJsonNode *old)
{
    BongoJsonNode *node = MemMalloc0(sizeof(BongoJsonNode));
    
    node->type = old->type;
    
    switch(old->type) {
    case BONGO_JSON_NULL :
        break;
    case BONGO_JSON_OBJECT :
        BongoJsonNodeAsObject(node) = BongoJsonObjectDup(BongoJsonNodeAsObject(old));
        break;
    case BONGO_JSON_ARRAY :
        BongoJsonNodeAsArray(node) = BongoJsonArrayDup(BongoJsonNodeAsArray(old));
        break;
    case BONGO_JSON_BOOL :
        BongoJsonNodeAsBool(node) = BongoJsonNodeAsBool(old);
        break;
    case BONGO_JSON_DOUBLE :
        BongoJsonNodeAsDouble(node) = BongoJsonNodeAsDouble(old);
        break;
    case BONGO_JSON_INT :
        BongoJsonNodeAsInt(node) = BongoJsonNodeAsInt(old);
        break;
    case BONGO_JSON_STRING :
        BongoJsonNodeAsString(node) = MemStrdup(BongoJsonNodeAsString(old));
        break;
    }

    return node;
}



char *
BongoJsonNodeToString(BongoJsonNode *node)
{
    BongoStringBuilder sb;
    
    BongoStringBuilderInit(&sb);
    BongoJsonNodeToStringBuilder(node, &sb);

    return sb.value;
}

#if PRETTY_JSON
static void
NewLine(BongoStringBuilder *sb, int depth)
{
    int i;
    BongoStringBuilderAppendChar(sb, '\n');
    for (i = 0; i < depth; i++) {
        BongoStringBuilderAppendChar(sb, '\t');
    }
}
#else
#define NewLine(s, n)
#endif

static void
BongoJsonNodeToStringBuilderInternal(BongoJsonNode *node, BongoStringBuilder *sb, int depth)
{
    switch(node->type) {
    case BONGO_JSON_NULL :
        BongoStringBuilderAppend(sb, "null");
        break;
    case BONGO_JSON_OBJECT :
        BongoJsonObjectToStringBuilderInternal(BongoJsonNodeAsObject(node), sb, depth);
        break;
    case BONGO_JSON_ARRAY :
        BongoJsonArrayToStringBuilderInternal(BongoJsonNodeAsArray(node), sb, depth);
        break;
    case BONGO_JSON_BOOL :
        BongoStringBuilderAppend(sb, BongoJsonNodeAsBool(node) ? "true" : "false");
        break;
    case BONGO_JSON_DOUBLE :
        BongoStringBuilderAppendF(sb, "%f", BongoJsonNodeAsDouble(node));
        break;
    case BONGO_JSON_INT :
        BongoStringBuilderAppendF(sb, "%d", BongoJsonNodeAsInt(node));
        break;
    case BONGO_JSON_STRING :
        BongoJsonQuoteStringToStringBuilder(BongoJsonNodeAsString(node), sb);
        break;
    }
}

void 
BongoJsonNodeToStringBuilder(BongoJsonNode *node, BongoStringBuilder *sb)
{
    BongoJsonNodeToStringBuilderInternal(node, sb, 0);
}

void 
BongoJsonNodeFree(BongoJsonNode *node)
{
    switch(node->type) {
    case BONGO_JSON_OBJECT :
        BongoJsonObjectFree(node->value.objectVal);
        break;
    case BONGO_JSON_ARRAY :
        BongoJsonArrayFree(node->value.arrayVal);
        break;
    case BONGO_JSON_STRING :
        MemFree(node->value.stringVal);
        break;
    default :
        break;
    }

    BongoJsonNodeFreeSteal(node);
}

void 
BongoJsonNodeFreeSteal(BongoJsonNode *node)
{
    MemFree(node);
}



static BongoJsonResult
BongoJsonLookup(BongoJsonNode *node, const char *name, BongoJsonNode **out)
{
    char *endp;
    unsigned int i;
    
    switch(node->type) {
    case BONGO_JSON_ARRAY :
        i = strtol(name, &endp, 10);
        if (*endp != '\0') {
            return BONGO_JSON_BAD_KEY;
        } else if (i >= BongoJsonNodeAsArray(node)->len) {
            return BONGO_JSON_BOUNDS_ERROR;
        }
        *out = BongoJsonArrayGet(BongoJsonNodeAsArray(node), i);
        return BONGO_JSON_OK;
    case BONGO_JSON_OBJECT :
        return BongoJsonObjectGet(BongoJsonNodeAsObject(node), name, out);
    default :
        return BONGO_JSON_BAD_KEY;
    }
    
}

static BongoJsonResult
BongoJsonNodeResolveSplit(BongoJsonNode *node, char **split, int n, BongoJsonNode **out)
{
    int i;

    *out = NULL;
    
    if (n < 1) {
        *out = node;
        return BONGO_JSON_OK;
    }

    for (i = 0; i < n; i++) {
        BongoJsonResult res;
        
        if ((res = BongoJsonLookup(node, split[i], &node)) != BONGO_JSON_OK) {
            return res;
        }   
    }

    *out = node;

    return BONGO_JSON_OK;
}

BongoJsonResult
BongoJsonNodeResolve(BongoJsonNode *node, const char *path, BongoJsonNode **out)
{
    BongoJsonResult res;
    
    char *copy;
    char *split[20];
    int n;

    copy = MemStrdup(path);
    
    n = BongoStringSplit(copy, '/', split, 20);
    res = BongoJsonNodeResolveSplit(node, split, n, out);

    MemFree(copy);

    return res;
}

/*** BongoJsonArray **/

BongoArray *
BongoJsonArrayDup(BongoArray *old)
{
    BongoArray *array;
    unsigned int i;

    array = BongoArrayNew(sizeof(BongoJsonNode*), old->len);

    for (i = 0; i < old->len; i++) {
        BongoJsonNode *node;
        node = BongoJsonNodeDup(BongoJsonArrayGet(old, i));
        BongoJsonArrayAppend(array, node);
    }

    return array;
}


void
BongoJsonArrayAppend(BongoArray *array, BongoJsonNode *node)
{
    BongoArrayAppendValue(array, node);
}

void 
BongoJsonArrayRemove(BongoArray *array, int i)
{
    BongoJsonNode *node;
    
    node = BongoJsonArrayGet(array, i);
    BongoArrayRemove(array, i);
    BongoJsonNodeFree(node);
}

void
BongoJsonArrayRemoveSteal(BongoArray *array, int i)
{
    BongoArrayRemove(array, i);

}

char *
BongoJsonArrayToString(BongoArray *array)
{
    BongoStringBuilder sb;
    
    BongoStringBuilderInit(&sb);
    BongoJsonArrayToStringBuilder(array, &sb);

    return sb.value;    
}

static void
BongoJsonArrayToStringBuilderInternal(BongoArray *array, BongoStringBuilder *sb, int depth)
{
    unsigned int i;
    BOOL first;    

    BongoStringBuilderAppendChar(sb, '[');
    
    first = TRUE;
    for (i = 0; i < array->len; i++) {
        if (!first) {
            BongoStringBuilderAppendChar(sb, ',');
        } else {
            first = FALSE;
        }
        
        BongoJsonNodeToStringBuilderInternal(BongoJsonArrayGet(array, i), sb, depth + 1);
    }
    
    BongoStringBuilderAppendChar(sb, ']');
}

void
BongoJsonArrayToStringBuilder(BongoArray *array, BongoStringBuilder *sb)
{
    BongoJsonArrayToStringBuilderInternal(array, sb, 0);
}

void
BongoJsonArrayFree(BongoArray *array)
{
    unsigned int i;

    for (i = 0; i < array->len; i++) {
        BongoJsonNode *node;

        node = BongoJsonArrayGet(array, i);
        BongoJsonNodeFree(node);
    }

    BongoArrayFree(array, TRUE);
}


/*** BongoJsonObject ***/

static void
KeyFree(void *data)
{
    MemFree(data);
}

BongoJsonObject *
BongoJsonObjectNew(void) 
{
    BongoHashtable *obj = BongoHashtableCreateFull(15, 
                                                 (HashFunction)BongoStringHash,
                                                 (CompareFunction)strcmp,
                                                 (DeleteFunction)KeyFree,
                                                 (DeleteFunction)BongoJsonNodeFree);

    return (BongoJsonObject *)obj;
}

BongoJsonObject *
BongoJsonObjectDup(BongoJsonObject *old)
{
    BongoJsonObject *obj;
    BongoJsonObjectIter iter;
    
    obj = BongoJsonObjectNew();
    
    for (BongoJsonObjectIterFirst(old, &iter);
         iter.key != NULL;
         BongoJsonObjectIterNext(old, &iter)) {
        BongoJsonObjectPut(obj, MemStrdup(iter.key), BongoJsonNodeDup(iter.value));
    }

    return obj;
}

void
BongoJsonObjectFree(BongoJsonObject *obj)
{
    BongoHashtableDelete((BongoHashtable*)obj);
}

void 
BongoJsonObjectToStringBuilderInternal(BongoJsonObject *obj, BongoStringBuilder *sb, int depth)
{
    BongoJsonObjectIter iter;
    BOOL first;
    
    NewLine(sb, depth);
    
    BongoStringBuilderAppendChar(sb, '{');

    first = TRUE;
    for (BongoJsonObjectIterFirst(obj, &iter); 
         iter.key != NULL;
         BongoJsonObjectIterNext(obj, &iter)) {
        
        if (!first) {
            BongoStringBuilderAppendChar(sb, ',');
        } else {
            first = FALSE;
        }

        NewLine(sb, depth + 1);

        BongoJsonQuoteStringToStringBuilder(iter.key, sb);
        BongoStringBuilderAppendChar(sb, ':');
        BongoJsonNodeToStringBuilderInternal(iter.value, sb, depth + 1);
    }
    NewLine(sb, depth);
    BongoStringBuilderAppendChar(sb, '}');
}

void
BongoJsonObjectToStringBuilder(BongoJsonObject *obj, BongoStringBuilder *sb)
{
    BongoJsonObjectToStringBuilderInternal(obj, sb, 0);
}

char *
BongoJsonObjectToString(BongoJsonObject *obj)
{
    BongoStringBuilder sb;
    
    BongoStringBuilderInit(&sb);
    BongoJsonObjectToStringBuilder(obj, &sb);

    return sb.value;    
}

BOOL
BongoJsonObjectIterFirst(BongoJsonObject *obj, BongoJsonObjectIter *iter)
{
    BOOL ret;

    ret = BongoHashtableIterFirst((BongoHashtable*)obj, &iter->iter);
    iter->key = (const char *)iter->iter.key;
    iter->value = (BongoJsonNode *)iter->iter.value;

    return ret;
}

BOOL
BongoJsonObjectIterNext(BongoJsonObject *obj, BongoJsonObjectIter *iter)
{
    BOOL ret;

    ret = BongoHashtableIterNext((BongoHashtable*)obj, &iter->iter);
    iter->key = (const char *)iter->iter.key;
    iter->value = (BongoJsonNode *)iter->iter.value;

    return ret;
}

BongoJsonResult
BongoJsonObjectGet(BongoJsonObject *obj, const char *key, BongoJsonNode **node)
{
    *node = BongoHashtableGet((BongoHashtable *)obj, key);

    if (*node) {
        return BONGO_JSON_OK;
    } else {
        return BONGO_JSON_NOT_FOUND;
    }
}

BongoJsonResult
BongoJsonObjectGetObject(BongoJsonObject *obj, const char *key, BongoJsonObject **val)
{
    BongoJsonNode *node;
    BongoJsonResult res;

    res = BongoJsonObjectGet(obj, key, &node);
    if (res != BONGO_JSON_OK) {
        *val = NULL;
        return res;
    }

    if (node->type != BONGO_JSON_OBJECT) {
        *val = NULL;
        return BONGO_JSON_BAD_TYPE;
    }

    *val = node->value.objectVal;

    return BONGO_JSON_OK;
}

BongoJsonResult 
BongoJsonObjectGetArray(BongoJsonObject *obj, const char *key, BongoArray **val)
{
    BongoJsonNode *node;
    BongoJsonResult res;

    res = BongoJsonObjectGet(obj, key, &node);
    if (res != BONGO_JSON_OK) {
        *val = NULL;
        return res;
    }

    if (node->type != BONGO_JSON_ARRAY) {
        *val = NULL;
        return BONGO_JSON_BAD_TYPE;
    }

    *val = node->value.arrayVal;

    return BONGO_JSON_OK;
}

BongoJsonResult
BongoJsonObjectGetBool(BongoJsonObject *obj, const char *key, BOOL *val)
{
    BongoJsonNode *node;
    BongoJsonResult res;

    res = BongoJsonObjectGet(obj, key, &node);
    if (res != BONGO_JSON_OK) {
        *val = FALSE;
        return res;
    }

    if (node->type != BONGO_JSON_BOOL) {
        *val = FALSE;
        return BONGO_JSON_BAD_TYPE;
    }

    *val = node->value.boolVal;

    return BONGO_JSON_OK;
}

BongoJsonResult
BongoJsonObjectGetDouble(BongoJsonObject *obj, const char *key, double *val)
{
    BongoJsonNode *node;
    BongoJsonResult res;

    res = BongoJsonObjectGet(obj, key, &node);
    if (res != BONGO_JSON_OK) {
        *val = 0.0;
        return res;
    }

    if (node->type != BONGO_JSON_DOUBLE) {
        *val = 0.0;
        return BONGO_JSON_BAD_TYPE;
    }

    *val = node->value.doubleVal;

    return BONGO_JSON_OK;    
}

BongoJsonResult
BongoJsonObjectGetInt(BongoJsonObject *obj, const char *key, int *val)
{
    BongoJsonNode *node;
    BongoJsonResult res;

    res = BongoJsonObjectGet(obj, key, &node);
    if (res != BONGO_JSON_OK) {
        *val = 0;
        return res;
    }

    if (node->type != BONGO_JSON_INT) {
        *val = 0;
        return BONGO_JSON_BAD_TYPE;
    }

    *val = node->value.intVal;

    return BONGO_JSON_OK;
}

BongoJsonResult
BongoJsonObjectGetString(BongoJsonObject *obj, const char *key, const char **val)
{
    BongoJsonNode *node;
    BongoJsonResult res;

    res = BongoJsonObjectGet(obj, key, &node);
    if (res != BONGO_JSON_OK) {
        *val = NULL;
        return res;
    }

    if (node->type != BONGO_JSON_STRING) {
        *val = NULL;
        return BONGO_JSON_BAD_TYPE;
    }

    *val = node->value.stringVal;

    return BONGO_JSON_OK;
}


BongoJsonResult
BongoJsonObjectPut(BongoJsonObject *obj, const char *key, BongoJsonNode *node)
{
    assert(node);

    BongoHashtablePut((BongoHashtable*)obj, MemStrdup(key), node);

    return BONGO_JSON_OK;
}


BongoJsonResult
BongoJsonObjectPutNull(BongoJsonObject *obj, const char *key)
{
    BongoJsonNode *node;
    
    node = BongoJsonNodeNewNull();
    
    BongoHashtablePut((BongoHashtable*)obj, MemStrdup(key), node);

    return BONGO_JSON_OK;
}


BongoJsonResult
BongoJsonObjectPutObject(BongoJsonObject *obj, const char *key, BongoJsonObject *val)
{
    BongoJsonNode *node;
    
    node = BongoJsonNodeNewObject(val);
    
    BongoHashtablePut((BongoHashtable*)obj, MemStrdup(key), node);

    return BONGO_JSON_OK;
}

BongoJsonResult
BongoJsonObjectPutArray(BongoJsonObject *obj, const char *key, BongoArray *val)
{
    BongoJsonNode *node;
    
    node = BongoJsonNodeNewArray(val);
    
    BongoHashtablePut((BongoHashtable*)obj, MemStrdup(key), node);

    return BONGO_JSON_OK;
}

BongoJsonResult
BongoJsonObjectPutBool(BongoJsonObject *obj, const char *key, BOOL val)
{
    BongoJsonNode *node;
    
    node = BongoJsonNodeNewBool(val);
    
    BongoHashtablePut((BongoHashtable*)obj, MemStrdup(key), node);

    return BONGO_JSON_OK;
}

BongoJsonResult
BongoJsonObjectPutDouble(BongoJsonObject *obj, const char *key, double val)
{
    BongoJsonNode *node;
    
    node = BongoJsonNodeNewDouble(val);
    
    BongoHashtablePut((BongoHashtable*)obj, MemStrdup(key), node);

    return BONGO_JSON_OK;

}

BongoJsonResult
BongoJsonObjectPutInt(BongoJsonObject *obj, const char *key, int val)
{
    BongoJsonNode *node;
    
    node = BongoJsonNodeNewInt(val);
    
    BongoHashtablePut((BongoHashtable*)obj, MemStrdup(key), node);

    return BONGO_JSON_OK;

}

BongoJsonResult
BongoJsonObjectPutString(BongoJsonObject *obj, const char *key, const char *val)
{
    BongoJsonNode *node;
    
    node = BongoJsonNodeNewString(val);
    
    BongoHashtablePut((BongoHashtable*)obj, MemStrdup(key), node);

    return BONGO_JSON_OK;
}

BongoJsonResult
BongoJsonObjectRemove(BongoJsonObject *obj, const char *key)
{
    BongoHashtableRemove((BongoHashtable*)obj, key);    
    return BONGO_JSON_OK;
}

BongoJsonResult
BongoJsonObjectRemoveSteal(BongoJsonObject *obj, const char *key)
{
    BongoHashtableRemoveFull((BongoHashtable*)obj, key, TRUE, FALSE);
    return BONGO_JSON_OK;
}

BongoJsonResult
BongoJsonObjectResolve(BongoJsonObject *obj, const char *path, BongoJsonNode **out)
{
    BongoJsonNode *node;
    BongoJsonResult res;
    char *copy;
    char *split[20];
    int n;

    *out = NULL;

    copy = MemStrdup(path);
    
    n = BongoStringSplit(copy, '/', split, 20);

    if (n < 1) {
        MemFree(copy);
        return BONGO_JSON_BAD_KEY;
    }

    res = BongoJsonObjectGet(obj, split[0], &node);
    
    if (!node) {
        MemFree(copy);
        return BONGO_JSON_NOT_FOUND;
    }

    if (n < 2) {
        *out = node;
        MemFree(copy);
        return res;
    }

    res = BongoJsonNodeResolveSplit(node, split + 1, n - 1, out);

    MemFree(copy);

    return res;
    
}

