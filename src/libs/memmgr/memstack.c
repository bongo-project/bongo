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

#include <memmgr.h>


static BongoMemChunk *
BongoMemChunkNew (size_t size)
{
    BongoMemChunk *result;

    result = MemMalloc(size + sizeof(struct _BongoMemChunk));
    if (!result) {
        return NULL;
    }

    result->curptr = &result->data[0];
    result->endptr = result->curptr + size;
    result->next = NULL;

    return result;
}


static size_t
BongoMemChunkSize (BongoMemChunk *chunk)
{
    return (unsigned long) chunk->endptr - (unsigned long) &chunk->data[0];
}


int
BongoMemStackInit (BongoMemStack *stack, size_t size)
{
    stack->chunk = BongoMemChunkNew(size);
    return stack->chunk ? 0 : -1;
}


void
BongoMemStackDestroy (BongoMemStack *stack)
{
    BongoMemChunk *chunk;
    BongoMemChunk *next;

    for (chunk = stack->chunk; chunk; chunk = next) {
        next = chunk->next;
        MemFree(chunk);
    }
    stack->chunk = NULL;
}


BongoMemStack *
BongoMemStackNew(size_t size)
{
    BongoMemStack *result;

    result = MemMalloc(sizeof(BongoMemStack));
    if (result) {
        if (BongoMemStackInit(result, size)) {
            MemFree(result);
        }
    }
    return result;
}


void 
BongoMemStackDelete(BongoMemStack *stack)
{
    BongoMemStackDestroy(stack);
    MemFree(stack);
}


void 
BongoMemStackReset(BongoMemStack *stack)
{
    BongoMemChunk *chunk;
    BongoMemChunk *next;

    for (chunk = stack->chunk; chunk; chunk = next) {
        next = chunk->next;
        if (next) {
            MemFree(chunk);
        } else {
            chunk->curptr = &chunk->data[0];
            chunk->next = NULL;
            stack->chunk = chunk;
            return;
        }
    }
}


void *
BongoMemStackAlloc(BongoMemStack *stack, size_t size)
{
    void *result;
    BongoMemChunk *chunk;
    size_t newsize;

    if (stack->chunk->curptr + size < stack->chunk->endptr) {
        result = stack->chunk->curptr;
        stack->chunk->curptr += size;
        return result;
    }
    
    newsize = BongoMemChunkSize(stack->chunk);
    if (newsize < size) {
        newsize = size;
    }
    newsize *= 2;
    
    chunk = BongoMemChunkNew(newsize);
    if (!chunk) {
        return NULL;
    }

    chunk->next = stack->chunk;
    stack->chunk = chunk;
    
    result = stack->chunk->curptr;
    stack->chunk->curptr += size;
    
    return result;
}


void
BongoMemStackPop(BongoMemStack *stack, void *obj)
{
    BongoMemChunk *chunk;
    BongoMemChunk *next;

    for (chunk = stack->chunk; chunk; chunk = next) {
        next = chunk->next;

        if (obj > (void *) chunk && obj < (void *) chunk->endptr) {
            chunk->curptr = obj;
            stack->chunk = chunk;
            return;
        }

        if (next) {
            MemFree(chunk);
        } else {
            chunk->curptr = &chunk->data[0];
            chunk->next = NULL;
            stack->chunk = chunk;
            return;
        }
    }
}


void *
BongoMemStackPush(BongoMemStack *stack, void *obj, size_t size)
{
    void *ptr;

    ptr = BONGO_MEMSTACK_ALLOC(stack, size);
    if (ptr) {
        memcpy(ptr, obj, size);
    }
    return ptr;
}


char *
BongoMemStackPushStr(BongoMemStack *stack, const char *str)
{
    void *ptr;
    size_t size;

    size = strlen(str) + 1;

    ptr = BONGO_MEMSTACK_ALLOC(stack, size);
    if (ptr) {
        memcpy(ptr, str, size);
    }
    return (char *) ptr;
}
