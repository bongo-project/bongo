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

#define SB_DEBUG 0

#if SB_DEBUG
/* Examine a buffer, used to generate warnings in valgrind */
static void sb_Examine(const char *data, int size)
{
    int i;
    printf("examining %d bytes\n", size);
    for (i = 0; i < size; i++) {
        if (data[i] == 125) {
            printf("blah");
        }
    }
}
#endif

/*
    initialize an already allocated string buffer to ""
*/
int
BongoStringBuilderInit(BongoStringBuilder *sb)
{
    return BongoStringBuilderInitSized(sb, 126);
}

int
BongoStringBuilderInitSized(BongoStringBuilder *sb, int size)
{
    /* FIXME: is this too harsh? 
        it is an error to call this with no sb */
    assert(sb);

    if (size > 0) {
        sb->value = MemMalloc0(size + 1);
        if (!sb->value) {
            return -1;
        }
        sb->value[0] = '\0';
    } 
    
    sb->len = 0;
    sb->size = size;
    
    return 0;
}

BongoStringBuilder *
BongoStringBuilderNewSized(int size)
{
    BongoStringBuilder *sb;

    sb = MemNew0(BongoStringBuilder, 1);
    if (sb) {
        if (BongoStringBuilderInitSized(sb, size)) {
            MemFree(sb);
            sb = NULL;
        }        
    }

    return sb;
}


BongoStringBuilder *
BongoStringBuilderNew(void)
{
    BongoStringBuilder *sb;
    
    sb = MemNew0(BongoStringBuilder, 1);
    if (sb) {
        if (BongoStringBuilderInit(sb)) {
            MemFree(sb);
            sb = NULL;
        }
    }
    
    return sb;
}


void
BongoStringBuilderDestroy(BongoStringBuilder *sb)
{
    /* don't try to free the value if it isn't set */
    if (sb->value) {
        MemFree(sb->value);
    }
}

char * 
BongoStringBuilderFree(BongoStringBuilder *sb, BOOL freeSegment)
{
    void *ret;
    if (freeSegment) {
        if (sb->value) {
            MemFree(sb->value);
        }
        ret = NULL; 
    } else {
        ret = sb->value;
    }
    
    MemFree(sb);

    return ret;
}

static void
BongoStringBuilderExpand(BongoStringBuilder *sb, unsigned int newLen)
{
    unsigned int newSize;

    /* FIXME: is this too harsh? */
    assert(sb);

    if (sb->size >= newLen) {
        return;
    }    
    
    if (sb->size == 0) {
        newSize = 126;
    } else {
        newSize = sb->size * 2;
    }
    
    while (newSize < newLen) {
        newSize *= 2;
    }
    
    sb->value = MemRealloc(sb->value, newSize);
    sb->size = newSize;
}

void
BongoStringBuilderAppendN(BongoStringBuilder *sb, const char *str, int len)
{
    /* FIXME: is this too harsh? */
    assert(sb);

    #if SB_DEBUG
        sb_Examine(str, len);
    #endif

    BongoStringBuilderExpand(sb, sb->len + len + 1);

    memcpy(sb->value + sb->len, str, len);
    sb->len += len;
    sb->value[sb->len] = '\0';
}

void
BongoStringBuilderAppend(BongoStringBuilder *sb, const char *str)
{
    BongoStringBuilderAppendN(sb, str, strlen(str));
}

void
BongoStringBuilderAppendChar(BongoStringBuilder *sb, char c)
{
    BongoStringBuilderAppendN(sb, &c, 1);
}


void 
BongoStringBuilderAppendVF(BongoStringBuilder *sb, const char *format, va_list args)
{
    char buf[1024];

    XplVsnprintf(buf, 1024, format, args);

    BongoStringBuilderAppend(sb, buf);
}


void 
BongoStringBuilderAppendF(BongoStringBuilder *sb, const char *format, ...)
{
    char buf[1024];
    int i;
    va_list ap;

    va_start(ap, format);
    i = XplVsnprintf(buf, 1024, format, ap);
    va_end(ap);

    BongoStringBuilderAppend(sb, buf);
}

