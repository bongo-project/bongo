/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2005 Novell, Inc. All Rights Reserved.
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

uint32_t
BongoStringHash (char *s)
{
    uint32_t b = 1;
    uint32_t h = 0;

    for (; *s; s++) {
        h += b * tolower(*s);
        b = (b << 5) - b;
    }
    return h;
}


void *
BongoHashtableGet(const BongoHashtable *table, const void *key)
{
    struct BongoHashtableEntry *entry;
    uint32_t hashcode;
    const CompareFunction compfunc = table->compfunc;

    hashcode = table->hashfunc(key);

    for (entry = table->buckets[hashcode % table->bucketcount];
         entry;
         entry = entry->next)
    {
        if (entry->key == key || 
            (compfunc && !compfunc(entry->key, key))) 
        {
            return entry->value;
        }
    }

    return NULL;
}

/* returns 0 on success */

int
BongoHashtablePutNoReplace(BongoHashtable *table, void *key, void *value)
{
    struct BongoHashtableEntry *entry;
    int bucket;

    entry = MemMalloc (sizeof(struct BongoHashtableEntry));
    if (!entry) {
        return -1;
    }

    entry->hashcode = table->hashfunc(key);
    entry->key = key;
    entry->value = value;

    bucket = entry->hashcode % table->bucketcount;
    entry->next = table->buckets[bucket];
    table->buckets[bucket] = entry;
    table->itemcount++;

    return 0;
}


/* returns 0 on success 
   if keydeletefunc is not null, it is used to free the old key
   if oldvalue is not null, returns previous value (if set)
*/

int
BongoHashtablePut(BongoHashtable *table, void *key, void *value)
{
    struct BongoHashtableEntry *entry;
    uint32_t hashcode;
    const CompareFunction compfunc = table->compfunc;

    assert(key != NULL);

    hashcode = table->hashfunc(key);

    for (entry = table->buckets[hashcode % table->bucketcount];
         entry;
         entry = entry->next)
    {
        if (entry->key == key || 
            (compfunc && !compfunc(entry->key, key))) 
        {
            if (table->keydeletefunc) {
                table->keydeletefunc (entry->key);
            }

            if (table->valuedeletefunc) {
                table->valuedeletefunc(entry->value);
            }
            
            entry->key = key;
            entry->value = value;
            return 0;
        }
    }
    return BongoHashtablePutNoReplace(table, key, value);
}


/* returns value */

void *
BongoHashtableRemoveFull(BongoHashtable *table, const void *key, BOOL deleteKey, BOOL deleteValue)
{
    struct BongoHashtableEntry *entry;
    struct BongoHashtableEntry *last;
    uint32_t hashcode;
    const CompareFunction compfunc = table->compfunc;

    hashcode = table->hashfunc(key);

    
    for (last = NULL, entry = table->buckets[hashcode % table->bucketcount];
         entry;
         last = entry, entry = entry->next)
    {
        if (entry->key == key || 
            (compfunc && !compfunc(entry->key, key))) 
        {
            void *value = entry->value;
            if (deleteKey && table->keydeletefunc) {
                table->keydeletefunc(entry->key);
            }
            if (deleteValue && table->valuedeletefunc) {
                table->valuedeletefunc(entry->value);
            }
            table->itemcount--;
            
            if (last) {
                last->next = entry->next;
            } else {
                table->buckets[hashcode % table->bucketcount] = entry->next;
            }

            MemFree(entry);
            return value;
        }
    }
    return NULL;
}


void *
BongoHashtableRemoveKeyValue(BongoHashtable *table, const void *key, void *value,
                            BOOL deleteKey, BOOL deleteValue)
{
    struct BongoHashtableEntry *entry;
    struct BongoHashtableEntry *last;
    uint32_t hashcode;
    const CompareFunction compfunc = table->compfunc;

    hashcode = table->hashfunc(key);

    
    for (last = NULL, entry = table->buckets[hashcode % table->bucketcount];
         entry;
         last = entry, entry = entry->next)
    {
        if ((entry->key == key || (compfunc && !compfunc(entry->key, key)))
          && (entry->value == value))
        {
            if (deleteKey && table->keydeletefunc) {
                table->keydeletefunc(entry->key);
            }
            if (deleteValue && table->valuedeletefunc) {
                table->valuedeletefunc(entry->value);
            }
            table->itemcount--;
            
            if (last) {
                last->next = entry->next;
            } else {
                table->buckets[hashcode % table->bucketcount] = entry->next;
            }

            MemFree(entry);
            return value;
        }
    }
    return NULL;
}


void *
BongoHashtableRemove(BongoHashtable *table, const void *key)
{
    return BongoHashtableRemoveFull(table, key, TRUE, TRUE);
}

BongoHashtable *
BongoHashtableCreateFull(int buckets, 
                        HashFunction hashfunc, CompareFunction compfunc,
                        DeleteFunction keydeletefunc, DeleteFunction valuedeletefunc)
{
    BongoHashtable *table;
    const int size = ALIGN_SIZE(sizeof(BongoHashtable), ALIGNMENT) + buckets * ALIGN_SIZE(sizeof(struct BongoHashtableEntry *), ALIGNMENT);
    
    table = MemMalloc(size);
    if (!table) { 
        return NULL;
    }
    memset(table, 0, size);

    table->bucketcount = buckets;
    table->hashfunc = hashfunc;
    table->compfunc = compfunc;
    table->keydeletefunc = keydeletefunc;
    table->valuedeletefunc = valuedeletefunc;

    //memset(&(table->buckets[0]), 0, buckets * ALIGN_SIZE(sizeof(struct BongoHashtableEntry *), ALIGNMENT));

    return table;
}

BongoHashtable *
BongoHashtableCreate(int buckets, 
                    HashFunction hashfunc, CompareFunction compfunc)
{
    return BongoHashtableCreateFull(buckets, hashfunc, compfunc, NULL, NULL);
}


void
BongoHashtableForeach(BongoHashtable *table, HashtableForeachFunc func, void *data)
{
    int i;
    struct BongoHashtableEntry *entry;

    for (i = 0; i < table->bucketcount; i++) {
        entry = table->buckets[i]; 
        while (entry) {
            func(entry->key, entry->value, data);
            entry = entry->next;
        }
    }
}

BOOL
BongoHashtableIterFirst(BongoHashtable *table, BongoHashtableIter *iter)
{
    for (iter->bucket = 0; iter->bucket < table->bucketcount; iter->bucket++) {
        iter->entry = table->buckets[iter->bucket];
        if (iter->entry) {
            iter->key = iter->entry->key;
            iter->value = iter->entry->value;
            return TRUE;
        }
    }

    iter->key = iter->value = NULL;
    return FALSE;
}

BOOL
BongoHashtableIterNext(BongoHashtable *table, BongoHashtableIter *iter)
{
    iter->entry = iter->entry->next;
    if (iter->entry) {
        iter->key = iter->entry->key;
        iter->value = iter->entry->value;
        return TRUE;
    }
        
    for (iter->bucket++; iter->bucket < table->bucketcount; iter->bucket++) {
        iter->entry = table->buckets[iter->bucket];
        if (iter->entry) {
            iter->key = iter->entry->key;
            iter->value = iter->entry->value;
            return TRUE;
        }
    }

    iter->key = iter->value = NULL;
    
    return FALSE;
}

int
BongoHashtableSize(BongoHashtable *table)
{
    return table->itemcount;
}

void
BongoHashtableClear(BongoHashtable *table)
{
    BongoHashtableClearFull(table, TRUE, TRUE);
}

void
BongoHashtableClearFull(BongoHashtable *table, BOOL deleteKeys, BOOL deleteValues)
{
    int i;
    struct BongoHashtableEntry *entry;

    for (i = 0; i < table->bucketcount; i++) {
        entry = table->buckets[i]; 
        while (entry) {
            struct BongoHashtableEntry *tmp = entry->next;
            if (deleteKeys && table->keydeletefunc) {
                table->keydeletefunc(entry->key);
            }
            if (deleteValues && table->valuedeletefunc) {
                table->valuedeletefunc(entry->value);
            }
            MemFree(entry);
            entry = tmp;
        }
        table->buckets[i] = NULL;
    }
    table->itemcount = 0;
}

void
BongoHashtableDelete(BongoHashtable *table) 
{
    BongoHashtableClear(table);
    MemFree(table);
}


