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

#include "mdbldapp.h"

/**
 * Functions for handling char ** arrays.
 */

inline char **
NewValArray(int size)
{
    char **vals;

    vals = malloc(sizeof(char *) * (size + 1));
    if (vals) {
        memset(vals, 0, sizeof(char *) * (size + 1));
    }
    return vals;
}

inline void
FreeValArray(char **vals)
{
    int i;

    for (i = 0; vals[i]; i++) {
        free(vals[i]);
        vals[i] = NULL;
    }
    /*free(vals);*/
}

inline int
CountValArray(char **vals)
{
    int i;

    for (i = 0; vals[i]; i++);
    return i;
}

inline char **
CopyValArray(char **vals)
{
    char **dup;
    int count;
    int i;

    count = CountValArray(vals);
    dup = NewValArray(count);
    if (dup) {
        for (i = 0; vals[i]; i++) {
            dup[i] = strdup(vals[i]);
            if (!dup[i]) {
                break;
            }
        }
        if (i < count) {
            FreeValArray(dup);
            free(dup);
            dup = NULL;
        }
    }
    return dup;
}

inline BOOL
ExpandValArray(char ***vals, int size)
{
    char **tmps;
    char **ptr;
    int count;
    int ext;

    count = CountValArray(*vals);
    ext = size - count;
    if (ext > 0) {
        tmps = realloc(*vals, (sizeof(char *) * (size + 1)));
        if (tmps) {
            ptr = tmps + count + 1;
            memset(ptr, 0, (sizeof(char *) * ext));
            *vals = tmps;
            return TRUE;
        }
    }
    return FALSE;
}

inline unsigned int
ValueStructToValArray(MDBValueStruct *v, char **vals)
{
    unsigned int i;

    for (i = 0; i < v->Used; i++) {
        vals[i] = strdup((char *)(v->Value[i]));
        if (!vals[i]) {
            break;
        }
    }
    if (i < v->Used) {
        FreeValArray(vals);
        return 0;
    }
    return i;
}

inline int
ValArrayToValueStruct(char **vals, MDBValueStruct *v)
{
    int i;
    int count;

    count = CountValArray(vals);
    for (i = 0; vals[i]; i++) {
        if (!MDBLDAPAddValue((unsigned char *)(vals[i]), v)) {
            break;
        }
    }
    if (i < count) {
        return 0;
    }
    return i;
}

inline char **
TranslateValArray(char **in, TranslateFunction transfunc, char *opt)
{
    char **out;
    int count;
    int i;

    count = CountValArray(in);
    out = NewValArray(count);

    if (out) {
        for (i = 0; in[i]; i++) {
            out[i] = transfunc(in[i], opt);
            if (!out[i]) {
                break;
            }
        }
        if (i < count) {
            FreeValArray(out);
            free(out);
            out = NULL;
        }
    }
    return out;
}

inline unsigned int
TranslateValueStructToValArray(MDBValueStruct *v, char **vals, 
                               TranslateFunction transfunc, char *opt)
{
    unsigned int i;

    for (i = 0; i < v->Used; i++) {
        vals[i] = transfunc((char *)(v->Value[i]), opt);
        if (!vals[i]) {
            break;
        }
    }
    if (i < v->Used) {
        FreeValArray(vals);
        return 0;
    }
    return i;
}

inline int
TranslateValArrayToValueStruct(char **vals, MDBValueStruct *v, 
                               TranslateFunction transfunc, char *opt)
{
    char *val;
    int count;
    int i;

    count = CountValArray(vals);
    for (i = 0; vals[i]; i++) {
        val = transfunc(vals[i], opt);
        if (val) {
            if (!MDBLDAPAddValue((unsigned char *)val, v)) {
                break;
            }
            free(val);
        } else {
            break;
        }
    }
    if (i < count) {
        return 0;
    }
    return i;
}
