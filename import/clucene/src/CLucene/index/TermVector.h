/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_termvector_h
#define _lucene_index_termvector_h

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "FieldInfos.h"

CL_NS_DEF(index)

/** Provides access to stored term vector of 
 *  a document field.
 */
class TermFreqVector:LUCENE_BASE {
public:
	virtual ~TermFreqVector(){
	}

  /**
   * 
   * @return The field this vector is associated with.
   * 
   */ 
  virtual const TCHAR* getField() = 0;
  
  /** 
   * @return The number of terms in the term vector.
   */
  virtual int32_t size() = 0;

  /** 
   * @return An Array of term texts in ascending order.
   */
  virtual const TCHAR** getTerms() = 0;


  /** Array of term frequencies. Locations of the array correspond one to one
   *  to the terms in the array obtained from <code>getTerms</code>
   *  method. Each location in the array contains the number of times this
   *  term occurs in the document or the document field.
   *
   *  The size of the returned array is size()
   *  @memory Returning a pointer to internal data. Do not delete.
   */
  virtual const int32_t* getTermFrequencies() = 0;
  

  /** Return an index in the term numbers array returned from
   *  <code>getTerms</code> at which the term with the specified
   *  <code>term</code> appears. If this term does not appear in the array,
   *  return -1.
   */
  virtual int32_t indexOf(const TCHAR* term) = 0;


  /** Just like <code>indexOf(int32_t)</code> but searches for a number of terms
   *  at the same time. Returns an array that has the same size as the number
   *  of terms searched for, each slot containing the result of searching for
   *  that term number.
   *
   *  @param terms array containing terms to look for
   *  @param start index in the array where the list of terms starts
   *  @param len the number of terms in the list
   */
  virtual const int32_t* indexesOf(const TCHAR** terms, const int32_t start, const int32_t len) = 0;

};



/**
 * Writer works by opening a document and then opening the fields within the document and then
 * writing out the vectors for each field.
 * 
 * Rough usage:
 *
 <CODE>
 for each document
 {
 writer.openDocument();
 for each field on the document
 {
 writer.openField(field);
 for all of the terms
 {
 writer.addTerm(...)
 }
 writer.closeField
 }
 writer.closeDocument()    
 }
 </CODE>
 */
class TermVectorsWriter:LUCENE_BASE {
private:
	class TVField:LUCENE_BASE {
	public:
		int32_t number;
		int64_t tvfPointer;
		int32_t length;   // number of distinct term positions

		TVField(int32_t number): tvfPointer(0),length(0) {
			this->number = number;
		}
        ~TVField(){}
	};

	class TVTerm:LUCENE_BASE {
		const TCHAR* termText;
		int32_t termTextLen; //textlen cache
	public:
		int32_t freq;
		const TCHAR* getTermText();
		size_t getTermTextLen();
		void setTermText(const TCHAR* val);
		TVTerm();
        ~TVTerm();
	};

    #define LUCENE_TVX_EXTENSION ".tvx"
    #define LUCENE_TVD_EXTENSION ".tvd"
    #define LUCENE_TVF_EXTENSION ".tvf"

  CL_NS(store)::IndexOutput* tvx, *tvd, *tvf;
  CL_NS(util)::CLVector<TVField*,CL_NS(util)::Deletor::Object<TVField> > fields;
  CL_NS(util)::CLVector<TVTerm*,CL_NS(util)::Deletor::Object<TVTerm> > terms;
  FieldInfos* fieldInfos;

  TVField* currentField;
  int64_t currentDocPointer;
  
  void addTermInternal(const TCHAR* termText, const int32_t freq);

  void writeField();
  void writeDoc();
  
public:
  LUCENE_STATIC_CONSTANT(int32_t, FORMAT_VERSION = 1);
  
  //The size in bytes that the FORMAT_VERSION will take up at the beginning of each file 
  LUCENE_STATIC_CONSTANT(int32_t, FORMAT_SIZE = 4);
  
  //TODO: Figure out how to write with or w/o position information and read back in

  /** Create term vectors writer for the specified segment in specified
   *  directory.  A new TermVectorsWriter should be created for each
   *  segment. The parameter <code>maxFields</code> indicates how many total
   *  fields are found in this document. Not all of these fields may require
   *  termvectors to be stored, so the number of calls to
   *  <code>openField</code> is less or equal to this number.
   */
  TermVectorsWriter(CL_NS(store)::Directory* directory, const char* segment,
                           FieldInfos* fieldInfos);

  ~TermVectorsWriter();
  void openDocument();
  void closeDocument();
  
  /** Close all streams. */
  void close();
  bool isDocumentOpen();
  
  /** Start processing a field. This can be followed by a number of calls to
   *  addTerm, and a final call to closeField to indicate the end of
   *  processing of this field. If a field was previously open, it is
   *  closed automatically.
   */
  void openField(const TCHAR* field);
  
  /** Finished processing current field. This should be followed by a call to
   *  openField before future calls to addTerm.
   */
  void closeField();
  
  /** Return true if a field is currently open. */
  bool isFieldOpen();
  
  /** Add specified vectors to the document.
   */
  void addVectors(TermFreqVector** vectors);
  
  /** Add term to the field's term vector. Field must already be open
   *  of NullPointerException is thrown. Terms should be added in
   *  increasing order of terms, one call per unique termNum. ProxPointer
   *  is a pointer into the TermPosition file (prx). Freq is the number of
   *  times this term appears in this field, in this document.
   */
  void addTerm(const TCHAR* termText, int32_t freq);
  
  /** Add specified vector to the document. Document must be open but no field
   *  should be open or exception is thrown. The same document can have <code>addTerm</code>
   *  and <code>addVectors</code> calls mixed, however a given field must either be
   *  populated with <code>addTerm</code> or with <code>addVector</code>.     *
   */
  void addTermFreqVector(TermFreqVector* vectr);
  void addTermFreqVectorInternal(TermFreqVector* vectr);
};

/**
 */
class SegmentTermVector: public TermFreqVector {
private:
  const TCHAR* field;
  const TCHAR** terms;
  int32_t termsLen; //cache
  int32_t* termFreqs;
  
  int32_t binarySearch(const TCHAR** a, const int32_t arraylen, const TCHAR* key) const;
public:
  SegmentTermVector(const TCHAR* field, const TCHAR** terms, int32_t* termFreqs);
  ~SegmentTermVector();

  /**
   * 
   * @return The number of the field this vector is associated with
   */
  const TCHAR* getField();
  TCHAR* toString();
  int32_t size();
  const TCHAR** getTerms();
  const int32_t* getTermFrequencies();
  int32_t indexOf(const TCHAR* termText);
  const int32_t* indexesOf(const TCHAR** termNumbers, const int32_t start, const int32_t len);
};



/** TODO: relax synchro!
 * Look at Java Lucene issue #30736 ([PATCH] to remove synchronized code from TermVectorsReader)
 */
class TermVectorsReader:LUCENE_BASE {
private:
  FieldInfos* fieldInfos;

  CL_NS(store)::IndexInput* tvx;
  CL_NS(store)::IndexInput* tvd;
  CL_NS(store)::IndexInput* tvf;
  int64_t _size;
  
  void checkValidFormat(CL_NS(store)::IndexInput* in);
  
  SegmentTermVector** readTermVectors(const TCHAR** fields, const int64_t* tvfPointers, const int32_t len);
  SegmentTermVector* readTermVector(const TCHAR* field, const int64_t tvfPointer);

  int64_t size();
  
  
	DEFINE_MUTEX(THIS_LOCK)
public:
  TermVectorsReader(CL_NS(store)::Directory* d, const char* segment, FieldInfos* fieldInfos);
  ~TermVectorsReader();

  void close();

  TermFreqVector* get(const int32_t docNum, const TCHAR* field);
  TermFreqVector** get(int32_t docNum);
};


class TermVectorOffsetInfo: LUCENE_BASE {
	static TermVectorOffsetInfo** _EMPTY_OFFSET_INFO;
    int startOffset;
    int endOffset;
public:
	static TermVectorOffsetInfo** EMPTY_OFFSET_INFO();

  TermVectorOffsetInfo();
  ~TermVectorOffsetInfo();
  TermVectorOffsetInfo(int32_t startOffset, int32_t endOffset);
  int32_t getEndOffset();
  void setEndOffset(int32_t endOffset);
  int32_t getStartOffset();
  void setStartOffset(int32_t startOffset);
  bool equals(TermVectorOffsetInfo* o);
  size_t hashCode();
};


/** Extends <code>TermFreqVector</code> to provide additional information about
 *  positions in which each of the terms is found.
 */
class TermPositionVector: public TermFreqVector {
public:

    /** Returns an array of positions in which the term is found.
     *  Terms are identified by the index at which its number appears in the
     *  term String array obtained from the <code>indexOf</code> method.
     *  May return null if positions have not been stored.
     */
    virtual int32_t* getTermPositions(int32_t index) = 0;
  
    /**
     * Returns an array of TermVectorOffsetInfo in which the term is found.
     * May return null if offsets have not been stored.
     * 
     * @see org.apache.lucene.analysis.Token
     * 
     * @param index The position in the array to get the offsets from
     * @return An array of TermVectorOffsetInfo objects or the empty list
     */ 
     virtual TermVectorOffsetInfo** getOffsets(int32_t index) = 0;
     
     virtual ~TermPositionVector(){
	 }
};

CL_NS_END
#endif
