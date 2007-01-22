/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_search_ChainedFilter_
#define _lucene_search_ChainedFilter_

#include "CLucene/index/IndexReader.h"
#include "CLucene/util/BitSet.h"
#include "CLucene/search/Filter.h"

CL_NS_DEF(search)

class ChainedFilter: public Filter 
{
public:	
	LUCENE_STATIC_CONSTANT(int, OR		= 0); //set current bit if the chain is set OR if the filter bit is set
	LUCENE_STATIC_CONSTANT(int, AND	= 1); //set current bit if the chain is set AND the filter bit is set
	LUCENE_STATIC_CONSTANT(int, ANDNOT	= 2); //set current bit if the chain is not set AND the filter bit is not set
	LUCENE_STATIC_CONSTANT(int, XOR	= 3); //set current bit if the chain is set OR the filter bit is set BUT not both is set
	
	LUCENE_STATIC_CONSTANT(int, USER	= 5); //add this value to user defined value, then override doUserChain

	LUCENE_STATIC_CONSTANT(int, DEFAULT = OR);
	
protected:
	Filter **filters;
	int	    *logicArray;
	int		 logic;
	
	ChainedFilter( const ChainedFilter& copy );
	CL_NS(util)::BitSet* bits( CL_NS(index)::IndexReader* reader, int logic );
	CL_NS(util)::BitSet* bits( CL_NS(index)::IndexReader* reader, int* logicArray );
	CL_NS(util)::BitSet* doChain( CL_NS(util)::BitSet* result, CL_NS(index)::IndexReader* reader, int logic, Filter* filter );

	virtual void doUserChain( CL_NS(util)::BitSet* chain, CL_NS(util)::BitSet* filter, int logic );
	virtual TCHAR* getLogicString(int logic);
public:
	ChainedFilter( Filter** filters, int op = DEFAULT );
	ChainedFilter( Filter** filters, int* _array );
	virtual ~ChainedFilter();
	
	/** Returns a BitSet with true for documents which should be permitted in
	search results, and false for those that should not. */
	CL_NS(util)::BitSet* bits( CL_NS(index)::IndexReader* reader );

	Filter* clone() const;
	
	TCHAR* toString();
};

CL_NS_END
#endif
