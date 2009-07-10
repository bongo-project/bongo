// memory allocation routines.

#include <config.h>
#include <xpl.h>
#include <glib.h>

EXPORT void *
MemMallocDirect(size_t Size, const char *SourceFile, unsigned long SourceLine)
{
	return g_malloc(Size);
}

EXPORT void *
MemMalloc0Direct(size_t size, const char *sourceFile, unsigned long sourceLine)
{
	return g_malloc0(size);
}

EXPORT void *
MemReallocDirect(void *Source, size_t Size, const char *SourceFile, unsigned long SourceLine)
{
	return g_realloc(Source, Size);
}

EXPORT char *
MemStrdupDirect(const char *StrSource, const char *SourceFile, unsigned long SourceLine)
{
	return MemStrndupDirect(StrSource, strlen(StrSource), SourceFile, SourceLine);
}

EXPORT char *
MemStrndupDirect(const char *StrSource, int n, const char *SourceFile, unsigned long SourceLine)
{
	char *new;
	
	new = MemMallocDirect(n + 1, SourceFile, SourceLine);
	memcpy(new, StrSource, n);
	new[n] = '\0';
	
	return new;
}

EXPORT void 
MemFreeDirect(void *Source, const char *SourceFile, unsigned long SourceLine)
{
	g_free(Source);
}

EXPORT void
MemFreeDirectCallback(void *Source)
{
	// this is used when we need a free()-like API to pass to other functions,
	// for example Hashtable.
	MemFreeDirect(Source, "unknown.c", 0);
	return;
}

#if defined(MEMMGR_DEBUG)

#endif
