/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_FieldsReader_
#define _lucene_index_FieldsReader_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/util/VoidMap.h"
#include "CLucene/store/Directory.h"
#include "CLucene/document/Document.h"
#include "CLucene/document/Field.h"
#include "FieldInfos.h"

CL_NS_DEF(index)

   /**
   * Class responsible for access to stored document fields.
   *
   * It uses &lt;segment&gt;.fdt and &lt;segment&gt;.fdx; files.
   */
	class FieldsReader :LUCENE_BASE{
	private:
		const FieldInfos* fieldInfos;
		CL_NS(store)::IndexInput* fieldsStream;
		CL_NS(store)::IndexInput* indexStream;
		int32_t _size;
	public:
		FieldsReader(CL_NS(store)::Directory* d, const char* segment, FieldInfos* fn);
		~FieldsReader();

		void close();

		int32_t size();

		CL_NS(document)::Document* doc(int32_t n);
	};
CL_NS_END
#endif
