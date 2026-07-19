'use client'

import {LegalPage, LegalSection} from '@/components/legal-page'
import {useTranslation} from 'react-i18next'

export default function TermsAddendumPage() {
    const {t} = useTranslation()
    return (
        <LegalPage
            title={t('legal.terms.v2.title')}
            updatedAt={t('legal.terms.v2.updatedAt')}
            updatedLabel={t('legal.updatedLabel')}
            metaDescription={t('legal.terms.v2.metaDescription')}
        >
            <p className="text-xs text-muted-foreground">
                {t('legal.version')} {t('legal.terms.v2.version')}
            </p>

            <p>
                {t('legal.terms.v2.intro1')}{' '}
                <a
                    href="https://accounts.aoctech.app/terms"
                    target="_blank"
                    rel="noreferrer"
                    className="underline underline-offset-4"
                >
                    {t('legal.terms.v2.termsOfUse')}
                </a>{' '}
                {t('legal.terms.v2.intro2')}{' '}
                <a
                    href="https://accounts.aoctech.app/privacy"
                    target="_blank"
                    rel="noreferrer"
                    className="underline underline-offset-4"
                >
                    {t('legal.terms.v2.privacy')}
                </a>{' '}
                {t('legal.terms.v2.intro3')}
            </p>

            <LegalSection heading={t('legal.terms.v2.s1.heading')}>
                <p>{t('legal.terms.v2.s1.p1')}</p>
                <p>{t('legal.terms.v2.s1.p2')}</p>
                <p>{t('legal.terms.v2.s1.p3')}</p>
                <p>{t('legal.terms.v2.s1.p4')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s2.heading')}>
                <p>{t('legal.terms.v2.s2.p1')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.terms.v2.s2.li1')}</li>
                    <li>{t('legal.terms.v2.s2.li2')}</li>
                    <li>{t('legal.terms.v2.s2.li3')}</li>
                </ul>
                <p>{t('legal.terms.v2.s2.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s3.heading')}>
                <p>{t('legal.terms.v2.s3.p1')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>
                        <strong>{t('legal.terms.v2.s3.li1b')}</strong>
                        {t('legal.terms.v2.s3.li1')}
                    </li>
                    <li>
                        <strong>{t('legal.terms.v2.s3.li2b')}</strong>
                        {t('legal.terms.v2.s3.li2')}
                    </li>
                    <li>
                        <strong>{t('legal.terms.v2.s3.li3b')}</strong>
                        {t('legal.terms.v2.s3.li3')}
                    </li>
                </ul>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s4.heading')}>
                <p>{t('legal.terms.v2.s4.p1')}</p>
                <p>{t('legal.terms.v2.s4.p2')}</p>
                <p>{t('legal.terms.v2.s4.p3')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.terms.v2.s4.li1')}</li>
                    <li>{t('legal.terms.v2.s4.li2')}</li>
                    <li>{t('legal.terms.v2.s4.li3')}</li>
                    <li>{t('legal.terms.v2.s4.li4')}</li>
                </ul>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s5.heading')}>
                <p>{t('legal.terms.v2.s5.p1')}</p>
                <p>{t('legal.terms.v2.s5.p2')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.terms.v2.s5.li1')}</li>
                    <li>{t('legal.terms.v2.s5.li2')}</li>
                    <li>{t('legal.terms.v2.s5.li3')}</li>
                </ul>
                <p>{t('legal.terms.v2.s5.p3')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s6.heading')}>
                <p>{t('legal.terms.v2.s6.p1')}</p>
                <p>{t('legal.terms.v2.s6.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s7.heading')}>
                <p>{t('legal.terms.v2.s7.p1')}</p>
                <p>{t('legal.terms.v2.s7.p2')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.terms.v2.s7.li1')}</li>
                    <li>{t('legal.terms.v2.s7.li2')}</li>
                    <li>{t('legal.terms.v2.s7.li3')}</li>
                    <li>{t('legal.terms.v2.s7.li4')}</li>
                    <li>{t('legal.terms.v2.s7.li5')}</li>
                    <li>{t('legal.terms.v2.s7.li6')}</li>
                </ul>
                <p>{t('legal.terms.v2.s7.p3')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s8.heading')}>
                <p>{t('legal.terms.v2.s8.p1')}</p>
                <p>{t('legal.terms.v2.s8.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s9.heading')}>
                <p>{t('legal.terms.v2.s9.p1')}</p>
                <p className="font-medium">{t('legal.terms.v2.s9.p2')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.terms.v2.s9.li1')}</li>
                    <li>{t('legal.terms.v2.s9.li2')}</li>
                    <li>{t('legal.terms.v2.s9.li3')}</li>
                    <li>{t('legal.terms.v2.s9.li4')}</li>
                    <li>{t('legal.terms.v2.s9.li5')}</li>
                    <li>{t('legal.terms.v2.s9.li6')}</li>
                </ul>
                <p>{t('legal.terms.v2.s9.p3')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s10.heading')}>
                <p>{t('legal.terms.v2.s10.p1')}</p>
                <p>{t('legal.terms.v2.s10.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s11.heading')}>
                <p>{t('legal.terms.v2.s11.p1')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.terms.v2.s11.li1')}</li>
                    <li>{t('legal.terms.v2.s11.li2')}</li>
                    <li>{t('legal.terms.v2.s11.li3')}</li>
                    <li>{t('legal.terms.v2.s11.li4')}</li>
                    <li>{t('legal.terms.v2.s11.li5')}</li>
                </ul>
                <p>{t('legal.terms.v2.s11.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s12.heading')}>
                <p>{t('legal.terms.v2.s12.p1')}</p>
                <p>{t('legal.terms.v2.s12.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v2.s13.heading')}>
                <p>{t('legal.terms.v2.s13.p1')}</p>
                <ul className="list-disc pl-5 space-y-2">
                    <li>{t('legal.terms.v2.s13.li1')}</li>
                    <li>{t('legal.terms.v2.s13.li2')}</li>
                    <li>{t('legal.terms.v2.s13.li3')}</li>
                    <li>{t('legal.terms.v2.s13.li4')}</li>
                    <li>{t('legal.terms.v2.s13.li5')}</li>
                </ul>
            </LegalSection>
        </LegalPage>
    )
}
