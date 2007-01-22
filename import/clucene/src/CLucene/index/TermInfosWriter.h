/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_TermInfosWriter_
#define _lucene_index_TermInfosWriter_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/store/Directory.h"
#include "FieldInfos.h"
#include "TermInfo.h"
#include "Term.h"

CL_NS_DEF(index)


	// This stores a monotonically increasing set of <Term, TermInfo> pairs in a
	// Directory.  A TermInfos can be written once, in order.  
	class TermInfosWriter :LUCENE_BASE{
	private:
		FieldInfos* fieldInfos;
		CL_NS(store)::IndexOutput* output;
		Term* lastTerm;
		TermInfo* lastTi;
		int64_t size;
		int64_t lastIndexPointer;
		bool isIndex;
		TermInfosWriter* other;
	
		//inititalize
		TermInfosWriter(CL_NS(store)::Directory* directory, const char* segment, FieldInfos* fis, bool isIndex);
	public:
		/** The file format version, a negative number. */
		LUCENE_STATIC_CONSTANT(int32_t,FORMAT=-2);

		/// <summary>Expert: The fraction of terms in the "dictionary" which should be stored
		/// in RAM.  Smaller values use more memory, but make searching slightly
		/// faster, while larger values use less memory and make searching slightly
		/// slower.  Searching is typically not dominated by dictionary lookup, so
		/// tweaking this is rarely useful.
		/// </summary>
		int32_t indexInterval;// = 128

		/// <summary>Expert: The fraction of {@link TermDocs} entries stored in skip tables,
		/// used to accellerate {@link TermDocs#SkipTo(int32_t)}.  Larger values result in
		/// smaller indexes, greater acceleration, but fewer accelerable cases, while
		/// smaller values result in bigger indexes, less acceleration and more
		/// accelerable cases. More detailed experiments would be useful here. 
		/// </summary>
		int32_t skipInterval;// = 16

	    //TODO2: how do you call another constructor... constructor here is initialising same variables as
	    //the private constructor. (This happens in a few other places as well).
		TermInfosWriter(CL_NS(store)::Directory* directory, const char* segment, FieldInfos* fis);

		~TermInfosWriter();

		// Adds a new <Term, TermInfo> pair to the set.
		//	Term must be lexicographically greater than all previous Terms added.
		//	TermInfo pointers must be positive and greater than all previous.
		void add(Term* term, const TermInfo* ti);

		// Called to complete TermInfos creation. 
		void close();

	private:
      //Helps constructors to initialize instances
		void initialise(CL_NS(store)::Directory* directory, const char* segment, bool IsIndex);


		void writeTerm(Term* term);
	};
CL_NS_END
#endif
