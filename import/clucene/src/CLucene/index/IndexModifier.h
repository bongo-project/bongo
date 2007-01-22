/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_index_IndexModifier_
#define _lucene_index_IndexModifier_

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

#include "CLucene/store/Directory.h"
#include "CLucene/document/Document.h"
#include "CLucene/analysis/AnalysisHeader.h"
#include "Term.h"
#include "IndexWriter.h"
#include "IndexReader.h"

CL_NS_DEF(index)

/// <summary> A class to modify an index, i.e. to delete and add documents. This
/// class hides {@link IndexReader} and {@link IndexWriter} so that you
/// do not need to care about implementation details such as that adding
/// documents is done via IndexWriter and deletion is done via IndexReader.
/// 
/// <p>Note that you cannot create more than one <code>IndexModifier</code> object
/// on the same directory at the same time.
/// 
/// <p>Example usage:
/// 
/// <div align="left" class="java">
/// <table border="0" cellpadding="3" cellspacing="0" bgcolor="#ffffff">
/// <tr>
/// <td nowrap="nowrap" valign="top" align="left">
/// <code>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#000000">Analyzer*&nbsp;analyzer&nbsp;=&nbsp;</font><font color="#7f0055"><b>new&nbsp;</b></font><font color="#000000">StandardAnalyzer</font><font color="#000000">()</font><font color="#000000">;</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#3f7f5f">//&nbsp;create&nbsp;an&nbsp;index&nbsp;in&nbsp;/tmp/index,&nbsp;overwriting&nbsp;an&nbsp;existing&nbsp;one:</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#000000">IndexModifier&nbsp;indexModifier&nbsp;=&nbsp;</font><font color="#7f0055"><b>new&nbsp;</b></font><font color="#000000">IndexModifier</font><font color="#000000">(</font><font color="#2a00ff">&#34;/tmp/index&#34;</font><font color="#000000">,&nbsp;analyzer,&nbsp;</font><font color="#7f0055"><b>true</b></font><font color="#000000">)</font><font color="#000000">;</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#000000">Document*&nbsp;doc&nbsp;=&nbsp;</font><font color="#7f0055"><b>new&nbsp;</b></font><font color="#000000">Document*</font><font color="#000000">()</font><font color="#000000">;</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#000000">doc.add</font><font color="#000000">(</font><font color="#7f0055"><b>new&nbsp;</b></font><font color="#000000">Field</font><font color="#000000">(</font><font color="#2a00ff">&#34;id&#34;</font><font color="#000000">,&nbsp;</font><font color="#2a00ff">&#34;1&#34;</font><font color="#000000">,&nbsp;Field.Store.YES,&nbsp;Field.Index.UN_TOKENIZED</font><font color="#000000">))</font><font color="#000000">;</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#000000">doc.add</font><font color="#000000">(</font><font color="#7f0055"><b>new&nbsp;</b></font><font color="#000000">Field</font><font color="#000000">(</font><font color="#2a00ff">&#34;body&#34;</font><font color="#000000">,&nbsp;</font><font color="#2a00ff">&#34;a&nbsp;simple&nbsp;test&#34;</font><font color="#000000">,&nbsp;Field.Store.YES,&nbsp;Field.Index.TOKENIZED</font><font color="#000000">))</font><font color="#000000">;</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#000000">indexModifier.addDocument</font><font color="#000000">(</font><font color="#000000">doc</font><font color="#000000">)</font><font color="#000000">;</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#7f0055"><b>int32_t&nbsp;</b></font><font color="#000000">deleted&nbsp;=&nbsp;indexModifier.delete</font><font color="#000000">(</font><font color="#7f0055"><b>new&nbsp;</b></font><font color="#000000">Term*</font><font color="#000000">(</font><font color="#2a00ff">&#34;id&#34;</font><font color="#000000">,&nbsp;</font><font color="#2a00ff">&#34;1&#34;</font><font color="#000000">))</font><font color="#000000">;</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#000000">System.out.println</font><font color="#000000">(</font><font color="#2a00ff">&#34;Deleted&nbsp;&#34;&nbsp;</font><font color="#000000">+&nbsp;deleted&nbsp;+&nbsp;</font><font color="#2a00ff">&#34;&nbsp;document&#34;</font><font color="#000000">)</font><font color="#000000">;</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#000000">indexModifier.flush</font><font color="#000000">()</font><font color="#000000">;</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#000000">System.out.println</font><font color="#000000">(</font><font color="#000000">indexModifier.docCount</font><font color="#000000">()&nbsp;</font><font color="#000000">+&nbsp;</font><font color="#2a00ff">&#34;&nbsp;docs&nbsp;in&nbsp;index&#34;</font><font color="#000000">)</font><font color="#000000">;</font><br/>
/// <font color="#ffffff">&nbsp;&nbsp;&nbsp;&nbsp;</font><font color="#000000">indexModifier.close</font><font color="#000000">()</font><font color="#000000">;</font></code>
/// </summary>
/// <summary></td>
/// </tr>
/// </table>
/// </div>
/// *
/// <p>Not all methods of IndexReader and IndexWriter are offered by this 
/// class. If you need access to additional methods, either use those classes 
/// directly or implement your own class that extends <code>IndexModifier</code>.
/// 
/// <p>Although an instance of this class can be used from more than one
/// thread, you will not get the best performance. You might want to use 
/// IndexReader and IndexWriter directly for that (but you will need to 
/// care about synchronization yourself then).
/// 
/// <p>While you can freely mix calls to add() and delete() using this class,
/// you should batch you calls for best performance. For example, if you
/// want to update 20 documents, you should first delete all those documents,
/// then add all the new documents.
/// 
/// </summary>
class IndexModifier :LUCENE_BASE{
protected:
	IndexWriter* indexWriter;
	IndexReader* indexReader;

	CL_NS(store)::Directory* directory;
	CL_NS(analysis)::Analyzer* analyzer;
	bool open;

	// Lucene defaults:
	bool useCompoundFile;
	//int32_t maxBufferedDocs;
	int32_t maxFieldLength;
	//int32_t mergeFactor;

public:

	/**
	* Open an index with write access.
	*
	* @param directory the index directory
	* @param analyzer the analyzer to use for adding new documents
	* @param create <code>true</code> to create the index or overwrite the existing one;
	* 	<code>false</code> to append to the existing index
	*/
	IndexModifier(CL_NS(store)::Directory* directory, CL_NS(analysis)::Analyzer* analyzer, bool create);
		
	~IndexModifier();

	/**
	* Open an index with write access.
	*
	* @param dirName the index directory
	* @param analyzer the analyzer to use for adding new documents
	* @param create <code>true</code> to create the index or overwrite the existing one;
	* 	<code>false</code> to append to the existing index
	*/
	IndexModifier(const char* dirName, CL_NS(analysis)::Analyzer* analyzer, bool create);

protected:

	/**
	* Initialize an IndexWriter.
	* @throws IOException
	*/
	void init(CL_NS(store)::Directory* directory, CL_NS(analysis)::Analyzer* analyzer, bool create);

	/**
	* Throw an IllegalStateException if the index is closed.
	* @throws IllegalStateException
	*/
	void assureOpen();

	/**
	* Close the IndexReader and open an IndexWriter.
	* @throws IOException
	*/
	void createIndexWriter();

	/**
	* Close the IndexWriter and open an IndexReader.
	* @throws IOException
	*/
	void createIndexReader();

public:
   DEFINE_MUTEX(THIS_LOCK);

	/**
	* Make sure all changes are written to disk.
	* @throws IOException
	*/
	void flush();

	/**
	* Adds a document to this index, using the provided analyzer instead of the
	* one specific in the constructor.  If the document contains more than
	* {@link #setMaxFieldLength(int32_t)} terms for a given field, the remainder are
	* discarded.
	* @see IndexWriter#addDocument(Document*, Analyzer*)
	* @throws IllegalStateException if the index is closed
	
	void addDocument(CL_NS(document)::Document* doc,  CL_NS(analysis)::Analyzer* docAnalyzer);
*/

	/**
	* Adds a document to this index.  If the document contains more than
	* {@link #setMaxFieldLength(int32_t)} terms for a given field, the remainder are
	* discarded.
	* @see IndexWriter#addDocument(CL_NS(document)::Document*)
	* @throws IllegalStateException if the index is closed
	*/
	void addDocument(CL_NS(document)::Document* doc);

	/**
	* Deletes all documents containing <code>term</code>.
	* This is useful if one uses a document field to hold a unique ID string for
	* the document.  Then to delete such a document, one merely constructs a
	* term with the appropriate field and the unique ID string as its text and
	* passes it to this method.  Returns the number of documents deleted.
	* @return the number of documents deleted
	* @see IndexReader#deleteDocuments(Term*)
	* @throws IllegalStateException if the index is closed
	*/
	int32_t deleteDocuments(Term* term);

	/**
	* Deletes the document numbered <code>docNum</code>.
	* @see IndexReader#deleteDocument(int32_t)
	* @throws IllegalStateException if the index is closed
	*/
	void deleteDocument(int32_t docNum);

	/**
	* Returns the number of documents currently in this index.
	* @see IndexWriter#docCount()
	* @see IndexReader#numDocs()
	* @throws IllegalStateException if the index is closed
	*/
	int32_t docCount();

	/**
	* Merges all segments together into a single segment, optimizing an index
	* for search.
	* @see IndexWriter#optimize()
	* @throws IllegalStateException if the index is closed
	*/
	void optimize();

	/**
	* Setting to turn on usage of a compound file. When on, multiple files
	* for each segment are merged into a single file once the segment creation
	* is finished. This is done regardless of what directory is in use.
	* @see IndexWriter#setUseCompoundFile(bool)
	* @throws IllegalStateException if the index is closed
	*/
	void setUseCompoundFile(bool useCompoundFile);

	/**
	* @throws IOException
	* @see IndexModifier#setUseCompoundFile(bool)
	*/
	bool getUseCompoundFile();

	/**
	* The maximum number of terms that will be indexed for a single field in a
	* document.  This limits the amount of memory required for indexing, so that
	* collections with very large files will not crash the indexing process by
	* running out of memory.<p/>
	* Note that this effectively truncates large documents, excluding from the
	* index terms that occur further in the document.  If you know your source
	* documents are large, be sure to set this value high enough to accomodate
	* the expected size.  If you set it to Integer.MAX_VALUE, then the only limit
	* is your memory, but you should anticipate an OutOfMemoryError.<p/>
	* By default, no more than 10,000 terms will be indexed for a field.
	* @see IndexWriter#setMaxFieldLength(int32_t)
	* @throws IllegalStateException if the index is closed
	*/
	void setMaxFieldLength(int32_t maxFieldLength);

	/**
	* @throws IOException
	* @see IndexModifier#setMaxFieldLength(int32_t)
	*/
	int32_t getMaxFieldLength();

	/**
	* The maximum number of terms that will be indexed for a single field in a
	* document.  This limits the amount of memory required for indexing, so that
	* collections with very large files will not crash the indexing process by
	* running out of memory.<p/>
	* Note that this effectively truncates large documents, excluding from the
	* index terms that occur further in the document.  If you know your source
	* documents are large, be sure to set this value high enough to accomodate
	* the expected size.  If you set it to Integer.MAX_VALUE, then the only limit
	* is your memory, but you should anticipate an OutOfMemoryError.<p/>
	* By default, no more than 10,000 terms will be indexed for a field.
	* @see IndexWriter#setMaxBufferedDocs(int32_t)
	* @throws IllegalStateException if the index is closed
	
	void setMaxBufferedDocs(int32_t maxBufferedDocs);

	/**
	* @throws IOException
	* @see IndexModifier#setMaxBufferedDocs(int32_t)
	
	int32_t getMaxBufferedDocs();*/

	/**
	* Determines how often segment indices are merged by addDocument().  With
	* smaller values, less RAM is used while indexing, and searches on
	* unoptimized indices are faster, but indexing speed is slower.  With larger
	* values, more RAM is used during indexing, and while searches on unoptimized
	* indices are slower, indexing is faster.  Thus larger values (&gt; 10) are best
	* for batch index creation, and smaller values (&lt; 10) for indices that are
	* interactively maintained.
	* <p>This must never be less than 2.  The default value is 10.
	*
	* @see IndexWriter#setMergeFactor(int32_t)
	* @throws IllegalStateException if the index is closed
	
	void setMergeFactor(int32_t mergeFactor);
*/
	/**
	* @throws IOException
	* @see IndexModifier#setMergeFactor(int32_t)
	
	int32_t getMergeFactor();
*/
	/**
	* Close this index, writing all pending changes to disk.
	*
	* @throws IllegalStateException if the index has been closed before already
	*/
	void close();

	const TCHAR* toString();	
	
	
	/**
	* Gets the version number of the currently open index.
	*/
	int64_t getCurrentVersion() const;
	
	/**
	* Returns an enumeration of all the documents which contain term. 
	*
	* Warning: This is not threadsafe. Make sure you lock the modifier object
	* while using the TermDocs. If the IndexReader that the modifier manages
	* is closed, the TermDocs object will fail.
	*/
	TermDocs* termDocs(Term* term=NULL);

	/**
	* Returns an enumeration of all terms after a given term.
	* If no term is given, an enumeration of all the terms 
	* in the index is returned.
	* The enumeration is ordered by Term.compareTo().  Each term
	* is greater than all that precede it in the enumeration.
	*
	* Warning: This is not threadsafe. Make sure you lock the modifier object
	* while using the TermDocs. If the IndexReader that the modifier manages
	* is closed, the Document will be invalid
	*/
	TermEnum* terms(Term* term=NULL);

	/**
	* Returns the stored fields of the n-th Document in this index. 
	*
	* Warning: This is not threadsafe. Make sure you lock the modifier object
	* while using the TermDocs. If the IndexReader that the modifier manages
	* is closed, the Document will be invalid
	*/
	CL_NS(document)::Document* document(const int32_t n);

	/**
	* Returns the directory used by this index.
	*/
	CL_NS(store)::Directory* getDirectory();
};
CL_NS_END
#endif


