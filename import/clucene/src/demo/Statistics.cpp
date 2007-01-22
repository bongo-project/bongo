/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "stdafx.h"

#include "CLucene.h"
#include "CLucene/index/Term.h"
#include "CLucene/index/SegmentTermEnum.h"
#include <iostream>

using namespace std;
using namespace lucene::analysis;
using namespace lucene::index;
using namespace lucene::util;
using namespace lucene::search;

void getStats(const char* directory){

	IndexReader* r = IndexReader::open(directory);
	_tprintf(_T("Statistics for %s\n"), directory);
	printf("==================================\n");

	printf("Max Docs: %d\n", r->maxDoc() );
	printf("Num Docs: %d\n", r->numDocs() );
	_tprintf(_T("Current Version: %d\n"), r->getCurrentVersion(directory) );
	
	TermEnum* te = r->terms();
	printf("Term count: %d\n\n", ((SegmentTermEnum*)te)->size );

	_CLDELETE(te);

	r->close();
	_CLDELETE(r);
}
