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
#include <xpl.h>
#include <bongoutil.h>
#include <assert.h>


int
BongoArrayInit(BongoArray *array, unsigned int elementSize, unsigned int initialSize)
{
    if (!array) {
        return -1;
    }

    if (initialSize > 0) {
        array->data = MemMalloc(initialSize * elementSize);
        if (!array->data) { 
            return -1;
        }
    }
    array->size = initialSize;
    array->elemSize = elementSize;
    array->len = 0;

    return 0;
}


void *
BongoArrayDestroy(BongoArray *array, BOOL freeSegment)
{
    void *ret;

    if (!array) {
        return NULL;
    }

    if (freeSegment) {
        MemFree(array->data);
        ret = NULL; 
    } else {
        ret = array->data;
    }
    
    return ret;
}


BongoArray *
BongoArrayNew(unsigned int elementSize, unsigned int initialSize)
{
    BongoArray *array;

    array = MemNew0(BongoArray, 1);
    
    if (array) {
        if (BongoArrayInit(array, elementSize, initialSize)) {
            MemFree(array);
            array = NULL;
        }
    }
     return array;
}


void * 
BongoArrayFree(BongoArray *array, BOOL freeSegment)
{
    if (!array) {
        return NULL;
    }

    void *ret = BongoArrayDestroy(array, freeSegment);

    MemFree(array);
    return ret;
}


static void
BongoArrayExpand(BongoArray *array, unsigned int newLen)
{
    unsigned int newSize;
    
    if (!array) {
        return;
    }

    if (array->size >= newLen) {
        return;
    }

    if (array->size == 0) {
        newSize = 10;
    } else {
        newSize = array->size * 2;
    }
    
    while (newSize < newLen) {
        newSize *= 2;
    }
    
    array->data = MemRealloc(array->data, newSize * array->elemSize);
    array->size = newSize;
}

void
BongoArrayAppendValues(BongoArray *array, const void *data, unsigned int len)
{
    if (!array) {
        return;
    }

    BongoArrayExpand(array, array->len + len);

    memcpy((char*)array->data + (array->elemSize * array->len), data, (array->elemSize * len));

    array->len += len;    
}

void 
BongoArrayRemove(BongoArray *array, unsigned int i)
{
    if (!array) {
        return;
    }

    if (i >= array->len) {
        return;
    }
    
    memmove((char *)array->data + (i * array->elemSize), 
            (char*)array->data + ((i + 1) * array->elemSize), 
            (array->elemSize * (array->len - i)));
    array->len--;
}

int
BongoArrayFindUnsorted(BongoArray *array, void *needle, ArrayCompareFunc compare)
{
    unsigned int i;

    if (!array) {
        return -1;
    }

    for (i = 0; i < array->len; i++) {
        if (!compare(needle, (char*)array->data + (i * array->elemSize))) {
            return (int)i;
        }
    }
    
    return -1;
}

int
BongoArrayFindSorted(BongoArray *array, void *needle, ArrayCompareFunc compare)
{
    void *data;

    if (!array) {
        return -1;
    }

    data = bsearch(needle, array->data, array->len, array->elemSize, compare);

    if (data) {
        return ((char*)data - (char*)array->data) / array->elemSize;
    } else {
        return -1;
    }
}

void
BongoArraySetLength(BongoArray *array, unsigned int len)
{
    if (!array) {
        return;
    }

    BongoArrayExpand(array, len);

    array->len = len;
}

void
BongoArraySort(BongoArray *array, ArrayCompareFunc compare)
{
    if (!array) {
        return;
    }

    qsort(array->data, array->len, array->elemSize, compare);
}


