/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_MultiReader
#define _lucene_index_MultiReader

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "SegmentHeader.h"
#include "IndexReader.h"
#include "CLucene/document/Document.h"
#include "Terms.h"
#include "SegmentMergeQueue.h"

CL_NS_DEF(index)

   /** An IndexReader which reads multiple indexes, appending their content.
   *
   * @version $Id: MultiReader.h 2240 2006-06-29 12:42:22Z ustramooner $
   */
	class MultiTermDocs:public virtual TermDocs {
	private:
	protected:
		TermDocs** readerTermDocs;

		IndexReader** subReaders;
      int32_t subReadersLength;
		const int32_t* starts;
		Term* term;

		int32_t base;
		int32_t pointer;

		TermDocs* current;              // == segTermDocs[pointer]
	public:
		MultiTermDocs();
		MultiTermDocs(IndexReader** subReaders, const int32_t* s);
		virtual ~MultiTermDocs();

		int32_t doc() const;
		int32_t freq() const;

        void seek(TermEnum* termEnum);
		void seek(Term* tterm);

		bool next();

  /** Optimized implementation. */
		int32_t read(int32_t* docs, int32_t* freqs, int32_t length);

  /** As yet unoptimized implementation. */
		bool skipTo(const int32_t target);

		void close();

		virtual TermPositions* __asTermPositions();
	protected:
		virtual TermDocs* termDocs(const IndexReader* reader) const;
	private:
		TermDocs* termDocs(const int32_t i) const;

	};


	//MultiTermEnum represents the enumeration of all terms of all readers
	class MultiTermEnum:public TermEnum {
	private:
		SegmentMergeQueue* queue;

		Term* _term;
		int32_t _docFreq;
	public:
		//Constructor
		//Opens all enumerations of all readers
		MultiTermEnum(IndexReader** subReaders, const int32_t* starts, const Term* t);

		//Destructor
		~MultiTermEnum();

		//Move the current term to the next in the set of enumerations
		bool next();

		//Returns the current term of the set of enumerations
		Term* term(const bool pointer = true);

		//Returns the document frequency of the current term in the set
		int32_t docFreq() const;

		//Closes the set of enumerations in the queue
		void close();
		
		const char* getObjectName(){ return MultiTermEnum::getClassName(); }
		static const char* getClassName(){  return "MultiTermEnum"; }
	};


	#pragma warning(disable : 4250) //todo: fix diamond problem
	class MultiTermPositions:public MultiTermDocs,public TermPositions {
	public:
		MultiTermPositions(IndexReader** subReaders, const int32_t* s);
		~MultiTermPositions() {};
		int32_t nextPosition();


		virtual TermDocs* __asTermDocs();
		virtual TermPositions* __asTermPositions();
	protected:
		TermDocs* termDocs(const IndexReader* reader) const;
	};


	class MultiReader:public IndexReader{
	private:
		bool _hasDeletions;
		IndexReader** subReaders;
		int32_t subReadersLength;
		int32_t* starts;			  // 1st docno for each segment

		CL_NS(util)::CLHashtable<const TCHAR*,uint8_t*,
			CL_NS(util)::Compare::TChar,
			CL_NS(util)::Deletor::tcArray,
			CL_NS(util)::Deletor::Array<uint8_t> > normsCache;
		int32_t _maxDoc;
		int32_t _numDocs;
		void initialize(IndexReader** subReaders);
      
		int32_t readerIndex(const int32_t n) const;
	protected:
		void doSetNorm(int32_t n, const TCHAR* field, uint8_t value);
		void doUndeleteAll();
		void doCommit();
		// synchronized
		void doClose();
		
		// synchronized
		void doDelete(const int32_t n);
	public:
		/** Construct reading the named set of readers. */
		MultiReader(CL_NS(store)::Directory* directory, SegmentInfos* sis, IndexReader** subReaders);
   
		/**
		* <p>Construct a MultiReader aggregating the named set of (sub)readers.
		* Directory locking for delete, undeleteAll, and setNorm operations is
		* left to the subreaders. </p>
		* <p>Note that all subreaders are closed if this Multireader is closed.</p>
		* @param subReaders set of (sub)readers
		* @throws IOException
		*/
		MultiReader(IndexReader** subReaders);
	
		~MultiReader();

		/** Return an array of term frequency vectors for the specified document.
		*  The array contains a vector for each vectorized field in the document.
		*  Each vector vector contains term numbers and frequencies for all terms
		*  in a given vectorized field.
		*  If no such fields existed, the method returns null.
		*/
		TermFreqVector** getTermFreqVectors(int32_t n);
		TermFreqVector* getTermFreqVector(int32_t n, const TCHAR* field);


		// synchronized
		int32_t numDocs();

		int32_t maxDoc() const;

		CL_NS(document)::Document* document(const int32_t n);

		bool isDeleted(const int32_t n);
		bool hasDeletions() { return _hasDeletions; }

		// synchronized
		uint8_t* norms(const TCHAR* field);
		void norms(const TCHAR* field, uint8_t* result, const int32_t offset);

		TermEnum* terms() const;
		TermEnum* terms(const Term* term) const;
		
		//Returns the document frequency of the current term in the set
		int32_t docFreq(const Term* t=NULL) const;
		TermDocs* termDocs() const;
		TermPositions* termPositions() const;
		
		/**
		* @see IndexReader#getFieldNames()
		*/
		TCHAR** getFieldNames();
		
		/**
		* @see IndexReader#getFieldNames(boolean)
		*/
		TCHAR** getFieldNames(bool indexed);
        
		TCHAR** getIndexedFieldNames(bool storedTermVector);
	};


CL_NS_END
#endif
