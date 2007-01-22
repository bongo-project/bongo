/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef UTIL_ENGLISH_H
#define UTIL_ENGLISH_H
#include "CLucene.h"

class English{
public:
    static void IntToEnglish(int32_t i, CL_NS(util)::StringBuffer* result);
    static TCHAR* IntToEnglish(int32_t i);
    static void IntToEnglish(int32_t i, TCHAR* buf, int32_t buflen);
};
#endif
