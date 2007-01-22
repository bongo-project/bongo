/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_DocumentWriter_
#define _lucene_index_DocumentWriter_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/analysis/AnalysisHeader.h"
#include "CLucene/document/Document.h"
#include "CLucene/store/Directory.h"
#include "FieldInfos.h"
#include "CLucene/util/VoidMap.h"
#include "CLucene/document/Field.h"
#include "TermInfo.h"
#include "CLucene/search/Similarity.h"
#include "TermInfosWriter.h"
#include "FieldsWriter.h"
#include "Term.h"

CL_NS_DEF(index)
	class Posting :LUCENE_BASE{				  // info about a Term in a doc
	public:
		int32_t *positions;				  // positions it occurs at
		int32_t positionsLength;
		Term* term;					  // the Term
		int32_t freq;					  // its frequency in doc
		
		int32_t getPositionsLength();
		Posting(Term* t, const int32_t position);
		~Posting();
	};


	class DocumentWriter :LUCENE_BASE{
	private:
		CL_NS(analysis)::Analyzer* analyzer;
		CL_NS(store)::Directory* directory;
		FieldInfos* fieldInfos; //array
		int32_t* fieldPositions; //array
		float_t* fieldBoosts; //array
		const int32_t maxFieldLength;
		CL_NS(search)::Similarity* similarity;

		// Keys are Terms, values are Postings.
		// Used to buffer a document before it is written to the index.
		CL_NS(util)::CLHashtable<Term*,Posting*,Term::Compare> postingTable;
		int32_t *fieldLengths; //array
		Term* termBuffer;

		  
	public:
   /**
   * 
   * @param directory The directory to write the document information to
   * @param analyzer The analyzer to use for the document
   * @param similarity The Similarity function
   * @param maxFieldLength The maximum number of tokens a field may have
   */ 
		DocumentWriter(CL_NS(store)::Directory* d, CL_NS(analysis)::Analyzer* a, CL_NS(search)::Similarity* similarity, const int32_t maxFieldLength);
		~DocumentWriter();

		void addDocument(const char* segment, CL_NS(document)::Document* doc);


	private:
		// Tokenizes the fields of a document into Postings.
		void invertDocument(CL_NS(document)::Document* doc);

		void addPosition(const TCHAR* field, const TCHAR* text, const int32_t position);

		void sortPostingTable(Posting**& Array, int32_t& arraySize);

		static void quickSort(Posting**& postings, const int32_t lo, const int32_t hi);

		void writePostings(Posting** postings, const int32_t postingsLength, const char* segment);

		void writeNorms(CL_NS(document)::Document* doc, const char* segment);

		void clearPostingTable();
	};


CL_NS_END
#endif
