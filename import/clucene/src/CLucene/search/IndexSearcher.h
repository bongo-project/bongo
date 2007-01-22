/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_search_IndexSearcher_
#define _lucene_search_IndexSearcher_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "SearchHeader.h"
#include "CLucene/store/Directory.h"
#include "CLucene/document/Document.h"
#include "CLucene/index/IndexReader.h"
#include "CLucene/index/Term.h"
#include "CLucene/util/BitSet.h"
#include "HitQueue.h"
#include "FieldSortedHitQueue.h"

CL_NS_DEF(search)
	
#ifndef LUCENE_HIDE_INTERNAL
	class SimpleTopDocsCollector:public HitCollector{ 
	private:
		float_t minScore;
		const CL_NS(util)::BitSet* bits;
		HitQueue* hq;
		const int32_t nDocs;
		int32_t* totalHits;
		const float_t ms;
	public:
		SimpleTopDocsCollector(const CL_NS(util)::BitSet* bs, HitQueue* hitQueue, int32_t* totalhits,const int32_t ndocs, const float_t minScore=-1.0f);
		~SimpleTopDocsCollector();
		void collect(const int32_t doc, const float_t score);
	};

	class SortedTopDocsCollector:public HitCollector{ 
	private:
		const CL_NS(util)::BitSet* bits;
		FieldSortedHitQueue* hq;
		const int32_t nDocs;
		int32_t* totalHits;
	public:
		SortedTopDocsCollector(const CL_NS(util)::BitSet* bs, FieldSortedHitQueue* hitQueue, int32_t* totalhits,const int32_t ndocs);
		~SortedTopDocsCollector();
		void collect(const int32_t doc, const float_t score);
	};

	class SimpleFilteredCollector: public HitCollector{
	private:
		CL_NS(util)::BitSet* bits;
		HitCollector* results;
	public:
		SimpleFilteredCollector(CL_NS(util)::BitSet* bs, HitCollector* collector);
		~SimpleFilteredCollector();
	protected:
		void collect(const int32_t doc, const float_t score);
	};
#endif //LUCENE_HIDE_INTERNAL

	/** Implements search over a single IndexReader.
	*
	* <p>Applications usually need only call the inherited {@link #search(Query)}
	* or {@link #search(Query,Filter)} methods.
	*/
	class IndexSearcher:public Searcher{
		CL_NS(index)::IndexReader* reader;
		bool readerOwner;

	public:
		// Creates a searcher searching the index in the named directory.
		IndexSearcher(const char* path);
      
		// Creates a searcher searching the index in the specified directory.
      IndexSearcher(CL_NS(store)::Directory* directory);

		// Creates a searcher searching the provided index. 
		IndexSearcher(CL_NS(index)::IndexReader* r);

		~IndexSearcher();
		    
		// Frees resources associated with this Searcher. 
		void close();

		int32_t docFreq(const CL_NS(index)::Term* term) const;
 
		CL_NS(document)::Document* doc(const int32_t i);

		int32_t maxDoc() const;

		TopDocs* _search(Query* query, Filter* filter, const int32_t nDocs);
		TopFieldDocs* _search(Query* query, Filter* filter, const int32_t nDocs, const Sort* sort);

		void _search(Query* query, Filter* filter, HitCollector* results);

		CL_NS(index)::IndexReader* getReader(){
			return reader;
		}

		Query* rewrite(Query* original);
		Explanation* explain(Query* query, int32_t doc);
	};
CL_NS_END
#endif
