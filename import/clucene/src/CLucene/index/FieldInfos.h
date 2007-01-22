/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_FieldInfos_
#define _lucene_index_FieldInfos_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/store/Directory.h"
#include "CLucene/document/Document.h"
#include "CLucene/document/Field.h"
#include "CLucene/util/VoidMap.h"
#include "CLucene/util/VoidList.h"
#include "FieldInfo.h"

CL_NS_DEF(index)

	
   /** Access to the Field Info file that describes document fields and whether or
   *  not they are indexed. Each segment has a separate Field Info file. Objects
   *  of this class are thread-safe for multiple readers, but only one thread can
   *  be adding documents at a time, with no other reader or writer threads
   *  accessing this object.
   */
	class FieldInfos :LUCENE_BASE{
	private:
		CL_NS(util)::CLArrayList<FieldInfo*,CL_NS(util)::Deletor::Object<FieldInfo> > byNumber;
		
		//we now use internd field names, so we can use the voidCompare
		//to directly compare the strings
		typedef CL_NS(util)::CLHashMap<const TCHAR*,FieldInfo*,
			CL_NS(util)::Compare::TChar > defByName;
		defByName byName;
	public:
		FieldInfos();
		~FieldInfos();

   /**
   * Construct a FieldInfos object using the directory and the name of the file
   * IndexInput
   * @param d The directory to open the IndexInput from
   * @param name The name of the file to open the IndexInput from in the Directory
   * @throws IOException
   * 
   * @see #read
   */
		FieldInfos(CL_NS(store)::Directory* d, const char* name);

		int32_t fieldNumber(const TCHAR* fieldName)const;


		FieldInfo* fieldInfo(const TCHAR* fieldName) const;
		const TCHAR* fieldName(const int32_t fieldNumber)const;

		FieldInfo* fieldInfo(const int32_t fieldNumber) const;

		int32_t size()const;

      	bool hasVectors() const;

		// Adds field info for a Document. 
		void add(CL_NS(document)::Document* doc);

		// Merges in information from another FieldInfos. 
		void add(FieldInfos* other);
		
		
	  /** If the field is not yet known, adds it. If it is known, checks to make
	   *  sure that the isIndexed flag is the same as was given previously for this
	   *  field. If not - marks it as being indexed.  Same goes for storeTermVector
	   * 
	   * @param name The name of the field
	   * @param isIndexed true if the field is indexed
	   * @param storeTermVector true if the term vector should be stored
	   */
		void add(const TCHAR* name, const bool isIndexed, const bool storeTermVector=false);
		
	   /**
	   * Assumes the field is not storing term vectors 
	   * @param names The names of the fields
	   * @param isIndexed true if the field is indexed
	   * @param storeTermVector true if the term vector should be stored
	   * 
	   * @see #add(String, boolean)
	   */
   		void add(const TCHAR** names, const bool isIndexed, const bool storeTermVector=false);

		void write(CL_NS(store)::Directory* d, const char* name);
		void write(CL_NS(store)::IndexOutput* output);

	private:
		void read(CL_NS(store)::IndexInput* input);
		void addInternal( const TCHAR* name,const bool isIndexed, const bool storeTermVector);

	};
CL_NS_END
#endif
