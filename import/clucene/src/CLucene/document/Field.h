/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_document_Field_
#define _lucene_document_Field_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/util/Reader.h"
#include "CLucene/debug/pool.h"

CL_NS_DEF(document)
  LUCENE_MP_DEFINE(Field)
  /**
  A field is a section of a Document.  Each field has two parts, a name and a
  value.  Values may be free text, provided as a String or as a Reader, or they
  may be atomic keywords, which are not further processed.  Such keywords may
  be used to represent dates, urls, etc.  Fields are optionally stored in the
  index, so that they may be returned with hits on the document.
  */
	class Field :LUCENE_POOLEDBASE(Field){
	private:
		const TCHAR* _name;
		TCHAR* _stringValue;
		CL_NS(util)::Reader* _readerValue;
		bool _isStored;
		bool _isIndexed;
		bool _isTokenized;
      bool storeTermVector;
      float_t boost;
	public:
		Field(const TCHAR* name, const TCHAR* value, bool store, bool index, bool token, const bool storeTermVector=false);
		Field(const TCHAR* name, CL_NS(util)::Reader* reader, bool store, bool index, bool token, const bool storeTermVector=false);
		Field(const TCHAR* name, CL_NS(util)::Reader* reader, const bool storeTermVector=false);

        ~Field();
        
		/** Constructs a String-valued Field that is not tokenized, but is indexed
      and stored.  Useful for non-text fields, e.g. date or url.  
      */
		static Field* Keyword(const TCHAR* name, const TCHAR* value);

		// Constructs a String-valued Field that is not tokenized nor indexed,
		//	but is stored in the index, for return with hits.
		static Field* UnIndexed(const TCHAR* name, const TCHAR* value);

		//  Constructs a String-valued Field that is tokenized and indexed,
		//	and is stored in the index, for return with hits.  Useful for short text
		//	fields, like "title" or "subject". 
		static Field* Text(const TCHAR* name, const TCHAR* value, const bool storeTermVector=false);

		//  Constructs a String-valued Field that is tokenized and indexed,
		//	but that is not stored in the index.
		static Field* UnStored(const TCHAR* name, const TCHAR* value, const bool storeTermVector=false);

		//  Constructs a Reader-valued Field that is tokenized and indexed, but is
		//	not stored in the index verbatim.  Useful for longer text fields, like
		//	"body".
		static Field* Text(const TCHAR* name, CL_NS(util)::Reader* value, const bool storeTermVector=false);

		//  The name of the field (e.g., "date", "subject", "title", "body", etc.)
		//	as an interned string. 
		const TCHAR* name(); ///<returns reference

		// The value of the field as a String, or null.  If null, the Reader value
		//	is used.  Exactly one of stringValue() and readerValue() must be set. 
		TCHAR* stringValue(); ///<returns reference

		//  The value of the field as a Reader, or null.  If null, the String value
		//	is used.  Exactly one of stringValue() and readerValue() must be set. 
		CL_NS(util)::Reader* readerValue();

		//  True iff the value of the field is to be stored in the index for return
		//	with search hits.  It is an error for this to be true if a field is
		//	Reader-valued. 
		bool isStored();

		//  True iff the value of the field is to be indexed, so that it may be
		//	searched on. 
		bool isIndexed();

		// True iff the value of the field should be tokenized as text prior to
		//	indexing.  Un-tokenized fields are indexed as a single word and may not be
		//	Reader-valued.
		bool isTokenized();

      /** True iff the term or terms used to index this field are stored as a term
      *  vector, available from {@link IndexReader#getTermFreqVector(int32_t,String)}.
      *  These methods do not provide access to the original content of the field,
      *  only to terms used to index it. If the original content must be
      *  preserved, use the <code>stored</code> attribute instead.
      *
      * @see IndexReader#getTermFreqVector(int32_t, String)
      */
      bool isTermVectorStored();

		  /** Returns the boost factor for hits on any field of this document.
		   *
		   * <p>The default value is 1.0.
		   *
		   * <p>Note: this value is not stored directly with the document in the index.
		   * Documents returned from {@link IndexReader#document(int32_t)} and {@link
		   * Hits#doc(int32_t)} may thus not have the same value present as when this field
		   * was indexed.
		   *
		   * @see #setBoost(float_t)
		   */
      float_t getBoost();
      
	   /** Sets the boost factor hits on this field.  This value will be
	   * multiplied into the score of all hits on this this field of this
	   * document.
	   *
	   * <p>The boost is multiplied by {@link Document#getBoost()} of the document
	   * containing this field.  If a document has multiple fields with the same
	   * name, all such values are multiplied together.  This product is then
	   * multipled by the value {@link Similarity#lengthNorm(String,int32_t)}, and
	   * rounded by {@link Similarity#encodeNorm(float_t)} before it is stored in the
	   * index.  One should attempt to ensure that this product does not overflow
	   * the range of that encoding.
	   *
	   * @see Document#setBoost(float_t)
	   * @see Similarity#lengthNorm(String, int32_t)
	   * @see Similarity#encodeNorm(float_t)
	   */
      void setBoost(float_t value);

		// Prints a Field for human consumption.
		TCHAR* toString();

	};
CL_NS_END
#endif
