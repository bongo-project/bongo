#include <config.h>
#include <xpl.h>
#include <glib.h>
#include "memmgrp.h"

struct MemPoolPrivateRef {
	size_t size;
	PoolEntryCB on_alloc;
	PoolEntryCB on_free;
	void *client_data;
};

EXPORT void *
MemPrivatePoolAlloc(unsigned char *name, size_t AllocationSize, unsigned int MinAllocCount, unsigned int MaxAllocCount, BOOL Dynamic, BOOL Temporary, PoolEntryCB allocCB, PoolEntryCB freeCB, void *clientData)
{
	struct MemPoolPrivateRef *ref;
	UNUSED_PARAMETER_REFACTOR(name);
	UNUSED_PARAMETER_REFACTOR(MinAllocCount);
	UNUSED_PARAMETER_REFACTOR(MaxAllocCount);
	UNUSED_PARAMETER_REFACTOR(Dynamic);
	UNUSED_PARAMETER_REFACTOR(Temporary);
	
	ref = g_new0(struct MemPoolPrivateRef, 1);
	ref->size = AllocationSize;
	ref->on_alloc = allocCB;
	ref->on_free = freeCB;
	ref->client_data = clientData;
	return (ref);
}

EXPORT void 
MemPrivatePoolFree(void *PoolHandle)
{
	// free this private pool - todo; keep references?
	g_free(PoolHandle);
	return;
}

EXPORT void *
MemPrivatePoolGetEntryDirect(void *PoolHandle, const char *SourceFile, unsigned long SourceLine)
{
	BOOL result = TRUE;
	struct MemPoolPrivateRef *ref = (struct MemPoolPrivateRef *)PoolHandle;
	UNUSED_PARAMETER_REFACTOR(SourceFile);
	UNUSED_PARAMETER_REFACTOR(SourceLine);
	
	void *new = g_slice_alloc0(ref->size);
	
	if (ref->on_alloc != NULL) {
		PoolEntryCB callback = ref->on_alloc;
		
		result = callback(new, ref->client_data);
	}
	if (result) {
		return new;
	} else {
		g_slice_free1(ref->size, new);
		return NULL;
	}
}

EXPORT void 
MemPrivatePoolReturnEntryDirect(void *PoolHandle, void *Source, const char *SourceFile, unsigned long SourceLine)
{
	struct MemPoolPrivateRef *ref = (struct MemPoolPrivateRef *)PoolHandle;
	UNUSED_PARAMETER_REFACTOR(SourceFile);
	UNUSED_PARAMETER_REFACTOR(SourceLine);
	
	if (ref->on_free != NULL) {
		PoolEntryCB callback = ref->on_free;
		
		callback(Source, ref->client_data);
	}
	
	g_slice_free1(ref->size, Source);
	return;
}
