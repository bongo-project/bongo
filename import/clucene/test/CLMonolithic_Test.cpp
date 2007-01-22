/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
/*
* this is a monolithic file that can be used to compile clucene tests using one source file.
* 
* note: when creating a project add either this file, or all the other .cpp files, not both!
*/

#include "CuTest.cpp"
#include "testall.cpp"
#include "tests.cpp"

#include "analysis/TestAnalysis.cpp"
#include "analysis/TestAnalyzers.cpp"
#include "index/TestHighFreqTerms.cpp"
#include "index/TestIndexWriter.cpp"
#include "index/TestReuters.cpp"
#include "index/TestUtf8.cpp"
#include "util/TestPriorityQueue.cpp"
#include "util/English.cpp"
#include "debug/TestError.cpp"
#include "queryParser/TestQueryParser.cpp"
#include "search/TestSearch.cpp"
#include "search/TestSort.cpp"
#include "search/TestTermVector.cpp"
#include "search/TestForDuplicates.cpp"
#include "search/TestDateFilter.cpp"
#include "search/TestWildcard.cpp"
#include "store/TestStore.cpp"
