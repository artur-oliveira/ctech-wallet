'use client'

import {LegalPage, LegalSection} from '@/components/legal-page'
import {useTranslation} from 'react-i18next'

// Keep in sync with wallet.CurrentTermsAddendumVersion in the Go API
// (api/internal/domain/wallet/user.go). The gate decision is server-side; this
// is the version the user is reading.
export default function TermsAddendumPage() {
    const {t} = useTranslation()
    return (
        <LegalPage
            title={t('legal.terms.v1.title')}
            updatedAt={t('legal.terms.v1.updatedAt')}
            updatedLabel={t('legal.updatedLabel')}
            metaDescription={t('legal.terms.v1.metaDescription')}
        >
            <p className="text-xs text-muted-foreground">
                {t('legal.version')} {t('legal.terms.v1.version')}
            </p>

            <p>
                {t('legal.terms.v1.intro1')}{' '}
                <a
                    href="https://accounts.aoctech.app/terms"
                    className="underline underline-offset-4"
                    target="_blank"
                    rel="noreferrer"
                >
                    {t('legal.terms.v1.termsOfUse')}
                </a>{' '}
                {t('legal.terms.v1.intro2')}{' '}
                <a
                    href="https://accounts.aoctech.app/privacy"
                    className="underline underline-offset-4"
                    target="_blank"
                    rel="noreferrer"
                >
                    {t('legal.terms.v1.privacy')}
                </a>{' '}
                {t('legal.terms.v1.intro3')}
            </p>

            <LegalSection heading={t('legal.terms.v1.s1.heading')}>
                <p>
                    {t('legal.terms.v1.s1.p1a')}
                    <strong>{t('legal.terms.v1.s1.p1Bold1')}</strong>
                    {t('legal.terms.v1.s1.p1b')}
                    <strong>{t('legal.terms.v1.s1.p1Bold2')}</strong>
                    {t('legal.terms.v1.s1.p1c')}
                </p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v1.s2.heading')}>
                <p>{t('legal.terms.v1.s2.p1')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v1.s3.heading')}>
                <p>{t('legal.terms.v1.s3.p1')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v1.s4.heading')}>
                <p>{t('legal.terms.v1.s4.p1')}</p>
                <p>{t('legal.terms.v1.s4.p2')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v1.s5.heading')}>
                <p>{t('legal.terms.v1.s5.p1')}</p>
                <p className="font-medium text-foreground">
                    {t('legal.terms.v1.s5.p2')}
                </p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v1.s6.heading')}>
                <p>{t('legal.terms.v1.s6.p1')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v1.s7.heading')}>
                <p>{t('legal.terms.v1.s7.p1')}</p>
            </LegalSection>

            <LegalSection heading={t('legal.terms.v1.s8.heading')}>
                <p>
                    {t('legal.terms.v1.s8.p1a')}
                    <a href="mailto:dpo@aoctech.app" className="underline underline-offset-4">
                        {t('legal.terms.v1.s8.dpoLink')}
                    </a>
                    {t('legal.terms.v1.s8.p1b')}
                </p>
            </LegalSection>
        </LegalPage>
    )
}
