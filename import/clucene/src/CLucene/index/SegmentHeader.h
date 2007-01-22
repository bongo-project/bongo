/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_SegmentHeader_
#define _lucene_index_SegmentHeader_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "SegmentInfos.h"
#include "CLucene/util/BitVector.h"
#include "CLucene/util/VoidMap.h"
#include "Term.h"
#include "FieldInfos.h"
#include "FieldsReader.h"
#include "IndexReader.h"
#include "TermInfosReader.h"
#include "CompoundFile.h"

CL_NS_DEF(index)
	class SegmentReader;

	class SegmentTermDocs:public virtual TermDocs {
	protected:
		// SegmentReader parent
		const SegmentReader* parent;

	private:
		int32_t count;
		int32_t df;

		int32_t _doc;

		CL_NS(util)::BitVector* deletedDocs;
		CL_NS(store)::IndexInput* freqStream;
		int32_t skipInterval;
		int32_t numSkips;
		int32_t skipCount;
		CL_NS(store)::IndexInput* skipStream;
		int32_t skipDoc;
		int64_t freqPointer;
		int64_t proxPointer;
		int64_t skipPointer;
		bool haveSkipped;
	public:
#ifndef LUCENE_HIDE_INTERNAL
		///For internal Use
		int32_t _freq;
#endif

		///\param Parent must be a segment reader
		SegmentTermDocs( SegmentReader* Parent);
		virtual ~SegmentTermDocs();

        virtual void seek(TermEnum* termEnum);
		virtual void seek(Term* term);
		virtual void seek(const TermInfo* ti);

		virtual void close();
		virtual int32_t doc()const;
		virtual int32_t freq()const;

		virtual bool next();

		/** Optimized implementation. */
		virtual int32_t read(int32_t* docs, int32_t* freqs, int32_t length);

		/** Optimized implementation. */
		virtual bool skipTo(const int32_t target);

		virtual TermPositions* __asTermPositions();
	protected:
		virtual void skippingDoc(){} //todo: check this... changed by bug report
		virtual void skipProx(int64_t proxPointer){}
	};





	class SegmentTermPositions: public SegmentTermDocs, public TermPositions {
	private:
		CL_NS(store)::IndexInput* proxStream;
		int32_t proxCount;
		int32_t position;

	public:
		///\param Parent must be a segment reader
		SegmentTermPositions(SegmentReader* Parent);
		~SegmentTermPositions();


		void close();
		int32_t nextPosition();
		bool next();
		int32_t read(int32_t* docs, int32_t* freqs, int32_t length);

		///\todo The virtual members required in TermPositions are defined in the subclass SegmentTermDocs,
		///This is because of the diamond inheritence because of the lack of
		///the interface concept
		///doubled up for now... very bad...
		void seek(Term* term);
      void seek(TermEnum* termEnum);
		void seek(const TermInfo* ti);
		int32_t doc() const;
		int32_t freq() const;
		int32_t read(int32_t docs[], int32_t freqs[]);
		bool skipTo(const int32_t target);

		virtual TermDocs* __asTermDocs();
		virtual TermPositions* __asTermPositions();
	protected:
		void skippingDoc();
  /** Called by super.skipTo(). */
        void skipProx(int64_t proxPointer);
	};



	///A SegementReader is an IndexReader responsible for reading 1 segment of an index
	class SegmentReader: public IndexReader{
		///The class Norm represents the normalizations for a field.
		///These normalizations are read from an IndexInput in into an array of bytes called bytes
		class Norm :LUCENE_BASE{
		private:
			int32_t number;
			SegmentReader* reader;
			const char* segment; ///< pointer to segment name
			public:
				CL_NS(store)::IndexInput* in;
				uint8_t* bytes;
				bool dirty;
				//Constructor
				Norm(CL_NS(store)::IndexInput* instrm, int32_t number, SegmentReader* reader, const char* segment);
				//Destructor
				~Norm();

				void reWrite();
		};

	private:
		//Holds the name of the segment that is being read
		const char* segment;

		//Indicates if there are documents marked as deleted
		bool   deletedDocsDirty;
		bool normsDirty;
		bool undeleteAll;


		//Holds all norms for all fields in the segment
		CL_NS(util)::CLHashtable<const TCHAR*,Norm*,CL_NS(util)::Compare::TChar> _norms; 

      
		// Compound File Reader when based on a compound file segment
		CompoundFileReader* cfsReader;
		///Reads the Field Info file
		FieldsReader* fieldsReader;
		TermVectorsReader* termVectorsReader;
      
		void initialize(SegmentInfo* si);

	protected:
		///Marks document docNum as deleted
		void doDelete(const int32_t docNum);
		void doUndeleteAll();
		void doCommit();
		void doSetNorm(int32_t doc, const TCHAR* field, uint8_t value);
	public:
#ifndef LUCENE_HIDE_INTERNAL
		///For Internal Use. a bitVector that manages which documents have been deleted
		CL_NS(util)::BitVector* deletedDocs;
		///For Internal Use. an IndexInput to the frequency file
		CL_NS(store)::IndexInput* freqStream;
		///For Internal Use. For reading the fieldInfos file
		FieldInfos* fieldInfos;
      ///For Internal Use. For reading the Term Dictionary .tis file
		TermInfosReader* tis;
		///For Internal Use. an IndexInput to the prox file
		CL_NS(store)::IndexInput* proxStream;
#endif

  /**
  Func - Constructor.
       Opens all files of a segment
       .fnm     -> Field Info File
                    Field names are stored in the field info file, with suffix .fnm.
       .frq     -> Frequency File
                   The .frq file contains the lists of documents which contain 
                   each term, along with the frequency of the term in that document.
       .prx     -> Prox File
                   The prox file contains the lists of positions that each term occurs
                   at within documents.
       .tis     -> Term Info File
                   This file is sorted by Term. Terms are ordered first lexicographically 
                   by the term's field name, and within that lexicographically by the term's text.
       .del     -> Deletion File
                   The .del file is optional, and only exists when a segment contains deletions
       .f[0-9]* -> Norm File
                   Contains s, for each document, a byte that encodes a value that is 
                   multiplied into the score for hits on that field:
  */
		SegmentReader(SegmentInfo* si);

		SegmentReader(SegmentInfos* sis, SegmentInfo* si);
		///Destructor.
		virtual ~SegmentReader();

		///Closes all streams to the files of a single segment
		void doClose();

		///Checks if a segment managed by SegmentInfo si has deletions
		static bool hasDeletions(const SegmentInfo* si);
      bool hasDeletions();

		///Returns all file names managed by this SegmentReader
		CL_NS(util)::AStringArrayConstWithDeletor* files();
		///Returns an enumeration of all the Terms and TermInfos in the set.
		TermEnum* terms() const;
		///Returns an enumeration of terms starting at or after the named term t
		TermEnum* terms(const Term* t) const;

		///\todo make const func
		///Returns a document identified by n
		CL_NS(document)::Document* document(const int32_t n);

		///\todo make const func
		///Checks if the n-th document has been marked deleted
		bool isDeleted(const int32_t n);

		///Returns an unpositioned TermDocs enumerator.
		TermDocs* termDocs() const;
		///Returns an unpositioned TermPositions enumerator.
		TermPositions* termPositions() const;

		///Returns the number of documents which contain the term t
		int32_t docFreq(const Term* t) const;

		///Returns the actual number of documents in the segment
		int32_t numDocs();
		///Returns the number of  all the documents in the segment including the ones that have
		///been marked deleted
		int32_t maxDoc() const;

      ///Returns the bytes array that holds the norms of a named field
		uint8_t* norms(const TCHAR* field);
		
      ///Reads the Norms for field from disk starting at offset in the inputstream
		void norms(const TCHAR* field, uint8_t* bytes, const int32_t offset);
		
		///concatenating segment with ext and x
		char* SegmentName(const char* ext, const int32_t x=-1);
      ///Creates a filename in buffer by concatenating segment with ext and x
		void SegmentName(char* buffer,int32_t bufferLen,const char* ext, const int32_t x=-1 );


      TCHAR** getFieldNames();
      TCHAR** getFieldNames(bool indexed);
      TCHAR** getIndexedFieldNames(bool storedTermVector);
      
    /** Return a term frequency vector for the specified document and field. The
   *  vector returned contains term numbers and frequencies for all terms in
   *  the specified field of this document, if the field had storeTermVector
   *  flag set.  If the flag was not set, the method returns null.
   */
      TermFreqVector* getTermFreqVector(int32_t docNumber, const TCHAR* field);
      
  /** Return an array of term frequency vectors for the specified document.
   *  The array contains a vector for each vectorized field in the document.
   *  Each vector vector contains term numbers and frequencies for all terms
   *  in a given vectorized field.
   *  If no such fields existed, the method returns null.
   */
      TermFreqVector** getTermFreqVectors(int32_t docNumber);
      static bool usesCompoundFile(SegmentInfo* si);
      static bool hasSeparateNorms(SegmentInfo* si);
	private:
		//Open all norms files for all fields
		void openNorms(CL_NS(store)::Directory* cfsDir);
		//Closes all norms files
		void closeNorms();
	};

	/*class SegmentReaderLockWith:public CL_NS(store)::LuceneLockWith{
	public:
		SegmentReader* reader;

		//Contructor
      SegmentReaderLockWith(CL_NS(store)::LuceneLock* lock, SegmentReader* rdr):
         CL_NS(store)::LuceneLockWith(lock)
      {
			this->reader = rdr;
		}

		//Flushes all deleted docs of reader to a new deletions file overwriting the current
		void* doBody();
	};*/


CL_NS_END
#endif
